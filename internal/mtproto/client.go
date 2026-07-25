// Package mtproto implements the Telegram adapter using gotd/td.
package mtproto

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/contrib/middleware/floodwait"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/internal/telegram"
)

// Client is a gotd-backed Telegram client. It lazily opens one MTProto
// connection and reuses it for every call until Close.
type Client struct {
	apiID       int
	apiHash     string
	sessionPath string
	waitFlood   bool
	maxWait     time.Duration

	mu          sync.Mutex
	channelHash map[int64]int64 // channel ID -> access hash

	connMu sync.Mutex
	conn   *conn
}

type conn struct {
	client *telegram.Client
	cancel context.CancelFunc
	ready  chan struct{}
	done   chan struct{}
	err    error
}

// New creates a real Telegram client. maxWait bounds flood-wait sleeps when
// waitFlood is enabled.
func New(apiID int64, apiHash, sessionPath string, waitFlood bool, maxWait time.Duration) *Client {
	if maxWait <= 0 {
		maxWait = 300 * time.Second
	}
	return &Client{
		apiID:       int(apiID),
		apiHash:     apiHash,
		sessionPath: sessionPath,
		waitFlood:   waitFlood,
		maxWait:     maxWait,
		channelHash: make(map[int64]int64),
	}
}

func (c *Client) ensureSessionDir() error {
	dir := filepath.Dir(c.sessionPath)
	if dir == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

const connectTimeout = 60 * time.Second

// ensureConn returns the live shared connection, dialing it on first use or
// after a previous connection died.
func (c *Client) ensureConn(ctx context.Context) (*conn, error) {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn != nil {
		select {
		case <-c.conn.done:
			c.conn = nil
		default:
			return c.conn, nil
		}
	}
	if err := c.ensureSessionDir(); err != nil {
		return nil, err
	}
	opts := telegram.Options{
		SessionStorage: &telegram.FileSessionStorage{Path: c.sessionPath},
		NoUpdates:      true,
	}
	var waiter *floodwait.Waiter
	if c.waitFlood {
		waiter = floodwait.NewWaiter().WithMaxWait(c.maxWait)
		opts.Middlewares = append(opts.Middlewares, waiter)
	}
	client := telegram.NewClient(c.apiID, c.apiHash, opts)
	runCtx, cancel := context.WithCancel(context.Background())
	cn := &conn{
		client: client,
		cancel: cancel,
		ready:  make(chan struct{}),
		done:   make(chan struct{}),
	}
	go func() {
		defer close(cn.done)
		run := func(ctx context.Context) error {
			return client.Run(ctx, func(ctx context.Context) error {
				close(cn.ready)
				<-ctx.Done()
				return ctx.Err()
			})
		}
		if waiter != nil {
			cn.err = waiter.Run(runCtx, run)
		} else {
			cn.err = run(runCtx)
		}
	}()
	select {
	case <-cn.ready:
		c.conn = cn
		return cn, nil
	case <-cn.done:
		cancel()
		if cn.err != nil {
			return nil, mapRPCError(cn.err)
		}
		return nil, errors.New("telegram connection closed before ready")
	case <-time.After(connectTimeout):
		cancel()
		return nil, errors.New("telegram connect timeout")
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}
}

// Close shuts down the shared connection.
func (c *Client) Close() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn != nil {
		c.conn.cancel()
		<-c.conn.done
		c.conn = nil
	}
	return nil
}

type runFn func(ctx context.Context, api *tg.Client, client *telegram.Client) error

func (c *Client) run(ctx context.Context, fn runFn) error {
	cn, err := c.ensureConn(ctx)
	if err != nil {
		return err
	}
	return fn(ctx, cn.client.API(), cn.client)
}

func (c *Client) rememberChannel(id, accessHash int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.channelHash[id] = accessHash
}

func (c *Client) channelAccessHash(channelID int64) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := c.channelHash[channelID]
	return h, ok
}

func (c *Client) setChannelAccessHash(channelID, accessHash int64) {
	c.rememberChannel(channelID, accessHash)
}

// RegisterChannelAccessHash seeds peer cache from DB.
func (c *Client) RegisterChannelAccessHash(channelID, accessHash int64) {
	c.setChannelAccessHash(channelID, accessHash)
}

func mapRPCError(err error) error {
	if err == nil {
		return nil
	}
	if wait, ok := tgerr.AsFloodWait(err); ok {
		return &tgtelegram.FloodWaitError{Seconds: int(wait.Seconds())}
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "auth_key_unregistered"), strings.Contains(msg, "session_password_needed"),
		strings.Contains(msg, "auth_restart"), strings.Contains(msg, "unauthorized"), strings.Contains(msg, "auth required"):
		return &tgtelegram.AuthRequiredError{}
	case strings.Contains(msg, "chat_write_forbidden"), strings.Contains(msg, "channel_private"),
		strings.Contains(msg, "chat_admin_required"), strings.Contains(msg, "message_delete_forbidden"):
		return &tgtelegram.PermissionDeniedError{}
	case strings.Contains(msg, "message_edit_time_expired"), strings.Contains(msg, "message_not_modified"),
		strings.Contains(msg, "message_author_required"), strings.Contains(msg, "not editable"):
		return &tgtelegram.MessageNotEditableError{}
	case strings.Contains(msg, "message_id_invalid"), strings.Contains(msg, "msg_id_invalid"):
		return &tgtelegram.MessageNotFoundError{}
	case strings.Contains(msg, "file_parts_invalid"), strings.Contains(msg, "file_part") && strings.Contains(msg, "too big"):
		return &tgtelegram.FileTooLargeError{}
	}
	return err
}

func parseChannelID(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty channel id")
	}
	if strings.HasPrefix(s, "-100") {
		v, err := strconv.ParseInt(s[4:], 10, 64)
		return v, err
	}
	return strconv.ParseInt(s, 10, 64)
}

func channelPeer(channelID, accessHash int64) tg.InputPeerClass {
	return &tg.InputPeerChannel{ChannelID: channelID, AccessHash: accessHash}
}

func userDisplay(u *tg.User) string {
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if name == "" && u.Username != "" {
		return u.Username
	}
	return name
}

func phoneNumber(u *tg.User) string {
	return u.Phone
}
