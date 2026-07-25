package fake

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// Client is an in-memory fake Telegram client for tests.
type Client struct {
	mu       sync.Mutex
	user     *telegram.User
	loggedIn bool
	channels map[int64]*telegram.Channel
	messages map[int64][]telegram.Message
	nextID   int
	nextChID int64
	code     string
	password string
	failCode bool

	failUpload bool
	failReply  bool
	failDelete bool
	denyPerms  bool
}

// New creates a fake client.
func New() *Client {
	return &Client{
		channels: make(map[int64]*telegram.Channel),
		messages: make(map[int64][]telegram.Message),
		nextID:   1,
		nextChID: 1000,
		code:     "12345",
	}
}

func (c *Client) SetCredentials(code, password string) {
	c.code = code
	c.password = password
}

func (c *Client) SetFailCode(v bool) { c.failCode = v }

// SetFailUpload makes UploadMedia fail (test hook).
func (c *Client) SetFailUpload(v bool) { c.failUpload = v }

// SetFailReply makes SendTextReply fail (test hook).
func (c *Client) SetFailReply(v bool) { c.failReply = v }

// SetFailDelete makes DeleteMessage fail (test hook).
func (c *Client) SetFailDelete(v bool) { c.failDelete = v }

// SetDenyPermissions makes mutating calls return PermissionDeniedError (test hook).
func (c *Client) SetDenyPermissions(v bool) { c.denyPerms = v }

func (c *Client) Login(ctx context.Context, apiID int64, apiHash, phone string, codeFn, passwordFn func() (string, error)) (*telegram.User, error) {
	if apiID == 0 || apiHash == "" || phone == "" {
		return nil, &telegram.AuthRequiredError{}
	}
	code, err := codeFn()
	if err != nil {
		return nil, err
	}
	if c.failCode || code != c.code {
		return nil, fmt.Errorf("invalid code")
	}
	if c.password != "" {
		pw, err := passwordFn()
		if err != nil {
			return nil, err
		}
		if pw != c.password {
			return nil, fmt.Errorf("invalid password")
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loggedIn = true
	c.user = &telegram.User{ID: 42, Phone: phone, DisplayName: "Test User"}
	return c.user, nil
}

func (c *Client) Status(ctx context.Context) (*telegram.User, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loggedIn {
		return nil, false, nil
	}
	return c.user, true, nil
}

func (c *Client) Logout(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loggedIn = false
	c.user = nil
	return nil
}

func (c *Client) CreateChannel(ctx context.Context, title string) (*telegram.Channel, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loggedIn {
		return nil, &telegram.AuthRequiredError{}
	}
	c.nextChID++
	ch := &telegram.Channel{ID: c.nextChID, Title: title, InviteLink: fmt.Sprintf("https://t.me/+fake%d", c.nextChID)}
	c.channels[ch.ID] = ch
	return ch, nil
}

func (c *Client) ResolveChannel(ctx context.Context, titleOrID string) (*telegram.Channel, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range c.channels {
		if ch.Title == titleOrID || fmt.Sprintf("%d", ch.ID) == titleOrID {
			return ch, nil
		}
	}
	return nil, fmt.Errorf("channel not found")
}

func (c *Client) BindChannel(ctx context.Context, titleOrID string) (*telegram.Channel, error) {
	return c.ResolveChannel(ctx, titleOrID)
}

func (c *Client) GetInviteLink(ctx context.Context, channelID int64) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.channels[channelID]
	if !ok {
		return "", fmt.Errorf("channel not found")
	}
	return ch.InviteLink, nil
}

func (c *Client) UploadMedia(ctx context.Context, req telegram.UploadRequest) (*telegram.UploadResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loggedIn {
		return nil, &telegram.AuthRequiredError{}
	}
	if c.denyPerms {
		return nil, &telegram.PermissionDeniedError{}
	}
	if c.failUpload {
		return nil, fmt.Errorf("upload failed")
	}
	data, err := io.ReadAll(req.Reader)
	if err != nil {
		return nil, err
	}
	if req.Size > 4*1024*1024*1024 {
		return nil, &telegram.FileTooLargeError{}
	}
	id := c.nextID
	c.nextID++
	msg := telegram.Message{ID: id, Caption: req.Caption, FileName: req.FileName, FileSize: req.Size, MIME: req.MIME, Data: data}
	c.messages[req.ChannelID] = append(c.messages[req.ChannelID], msg)
	return &telegram.UploadResult{MessageID: id}, nil
}

func (c *Client) SendTextReply(ctx context.Context, channelID int64, replyTo int, text string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failReply {
		return 0, fmt.Errorf("reply failed")
	}
	id := c.nextID
	c.nextID++
	rt := replyTo
	c.messages[channelID] = append(c.messages[channelID], telegram.Message{ID: id, Text: text, ReplyTo: &rt})
	return id, nil
}

func (c *Client) EditCaption(ctx context.Context, channelID int64, messageID int, caption string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.denyPerms {
		return &telegram.PermissionDeniedError{}
	}
	for i, m := range c.messages[channelID] {
		if m.ID == messageID {
			if m.NotEditable {
				return &telegram.MessageNotEditableError{}
			}
			c.messages[channelID][i].Caption = caption
			return nil
		}
	}
	return &telegram.MessageNotFoundError{}
}

func (c *Client) EditText(ctx context.Context, channelID int64, messageID int, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, m := range c.messages[channelID] {
		if m.ID == messageID {
			c.messages[channelID][i].Text = text
			return nil
		}
	}
	return &telegram.MessageNotFoundError{}
}

func (c *Client) DeleteMessage(ctx context.Context, channelID int64, messageID int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.denyPerms {
		return &telegram.PermissionDeniedError{}
	}
	if c.failDelete {
		return fmt.Errorf("delete failed")
	}
	msgs := c.messages[channelID]
	for i, m := range msgs {
		if m.ID == messageID {
			c.messages[channelID] = append(msgs[:i], msgs[i+1:]...)
			return nil
		}
	}
	return &telegram.MessageNotFoundError{}
}

func (c *Client) DownloadMedia(ctx context.Context, channelID int64, messageID int, dst io.Writer) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.messages[channelID] {
		if m.ID == messageID {
			_, err := dst.Write(m.Data)
			return err
		}
	}
	return &telegram.MessageNotFoundError{}
}

func (c *Client) Doctor(ctx context.Context, channelID int64) (*telegram.Capabilities, error) {
	return &telegram.Capabilities{
		AuthOK:           c.loggedIn,
		ChannelOK:        true,
		UploadOK:         true,
		DeleteOK:         true,
		InviteLinkOK:     true,
		EditOldCaptionOK: true,
		MaxUploadBytes:   2147483648,
		CheckedAt:        time.Now().UTC(),
	}, nil
}

func (c *Client) History(ctx context.Context, channelID int64, afterID int, limit int) ([]telegram.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []telegram.Message
	for _, m := range c.messages[channelID] {
		if m.ID > afterID {
			out = append(out, m)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Messages returns all messages for a channel (test helper).
func (c *Client) Messages(channelID int64) []telegram.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]telegram.Message(nil), c.messages[channelID]...)
}

// SetNotEditable marks a message as not editable.
func (c *Client) SetNotEditable(channelID int64, messageID int, v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, m := range c.messages[channelID] {
		if m.ID == messageID {
			c.messages[channelID][i].NotEditable = v
		}
	}
}
