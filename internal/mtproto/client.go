// Package mtproto implements the Telegram adapter using gotd/td.
package mtproto

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/go-faster/errors"
	"github.com/gotd/contrib/middleware/floodwait"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/internal/telegram"
)

// Client is a gotd-backed Telegram client.
type Client struct {
	apiID       int
	apiHash     string
	sessionPath string
	waitFlood   bool

	mu          sync.Mutex
	channelHash map[int64]int64 // channel ID -> access hash
}

// New creates a real Telegram client.
func New(apiID int64, apiHash, sessionPath string, waitFlood bool) *Client {
	return &Client{
		apiID:       int(apiID),
		apiHash:     apiHash,
		sessionPath: sessionPath,
		waitFlood:   waitFlood,
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

func (c *Client) telegramOptions() telegram.Options {
	return telegram.Options{
		SessionStorage: &telegram.FileSessionStorage{Path: c.sessionPath},
		NoUpdates:      true,
	}
}

type runFn func(ctx context.Context, api *tg.Client, client *telegram.Client) error

func (c *Client) run(ctx context.Context, fn runFn) error {
	if err := c.ensureSessionDir(); err != nil {
		return err
	}
	client := telegram.NewClient(c.apiID, c.apiHash, c.telegramOptions())
	run := func(ctx context.Context) error {
		return client.Run(ctx, func(ctx context.Context) error {
			return fn(ctx, client.API(), client)
		})
	}
	if c.waitFlood {
		waiter := floodwait.NewWaiter()
		return waiter.Run(ctx, run)
	}
	return run(ctx)
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
	case strings.Contains(msg, "auth"):
		return &tgtelegram.AuthRequiredError{}
	case strings.Contains(msg, "chat_write_forbidden"), strings.Contains(msg, "channel_private"):
		return &tgtelegram.PermissionDeniedError{}
	case strings.Contains(msg, "message_not_modified"), strings.Contains(msg, "not editable"):
		return &tgtelegram.MessageNotEditableError{}
	case strings.Contains(msg, "message_id_invalid"), strings.Contains(msg, "msg_id_invalid"):
		return &tgtelegram.MessageNotFoundError{}
	case strings.Contains(msg, "file_part") && strings.Contains(msg, "too big"):
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
