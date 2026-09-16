package fake

import (
	"context"
	"io"
	"sort"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// Saved Messages support for the fake client (td import saved).
//
// The saved chat is not a channel and has no id of its own, so it borrows the
// reserved messages-map key below — a key no fake channel ever takes. Seeding,
// persistence, downloads, and deletes then reuse the channel machinery
// unchanged, and a saved item is distinguished from a drive item only by the
// map it lives in.

// SavedChatKey is the reserved messages-map key of the saved chat.
const SavedChatKey int64 = 0

// AddSavedMessage seeds one message into the saved chat (test hook).
func (c *Client) AddSavedMessage(msg telegram.Message) telegram.Message {
	return c.AddMessage(SavedChatKey, msg)
}

// SavedMessages returns the saved chat's messages in chronological order
// (test helper).
func (c *Client) SavedMessages() []telegram.Message {
	return c.Messages(SavedChatKey)
}

// SetSavedUnavailable makes the saved chat unreadable, so doctor reports the
// saved capabilities as missing (test hook).
func (c *Client) SetSavedUnavailable(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.savedUnavailable = v
}

// SetFailSavedDelete makes DeleteSavedMessage fail (test hook). Deleting your
// own saved messages is always permitted on Telegram, so the saved chat needs
// its own failure knob instead of the channel permission knob.
func (c *Client) SetFailSavedDelete(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failSavedDelete = v
}

func (c *Client) StreamSavedHistory(ctx context.Context, afterID int, fn func(telegram.Message) error) (telegram.HistoryMeta, error) {
	c.mu.Lock()
	if !c.loggedIn {
		c.mu.Unlock()
		return telegram.HistoryMeta{}, &telegram.AuthRequiredError{}
	}
	if c.savedUnavailable {
		c.mu.Unlock()
		return telegram.HistoryMeta{}, &telegram.PermissionDeniedError{}
	}
	msgs, complete := c.historyLocked(SavedChatKey, afterID, 0)
	total := len(c.messages[SavedChatKey])
	c.mu.Unlock()

	meta := telegram.HistoryMeta{Complete: complete, TotalMessages: total}
	for _, m := range msgs {
		if meta.OldestID == 0 || m.ID < meta.OldestID {
			meta.OldestID = m.ID
		}
		if err := fn(m); err != nil {
			return telegram.HistoryMeta{}, err
		}
	}
	return meta, nil
}

func (c *Client) GetSavedMessage(ctx context.Context, messageID int) (telegram.Message, error) {
	return c.GetMessage(ctx, SavedChatKey, messageID)
}

func (c *Client) DownloadSavedMedia(ctx context.Context, messageID int, dst io.Writer) error {
	return c.DownloadMedia(ctx, SavedChatKey, messageID, dst)
}

func (c *Client) DeleteSavedMessage(ctx context.Context, messageID int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failSavedDelete {
		return &telegram.PermissionDeniedError{}
	}
	msgs := c.messages[SavedChatKey]
	for i, m := range msgs {
		if m.ID == messageID {
			c.messages[SavedChatKey] = append(msgs[:i], msgs[i+1:]...)
			c.save()
			return nil
		}
	}
	return &telegram.MessageNotFoundError{}
}

// SeedSavedAlbum seeds a forwarded media album into the saved chat: members
// share one grouped id, only the first carries the caption, and every member
// carries the same forward header and sub-chat — exactly what a real saved
// album looks like (test helper). It returns the seeded messages.
func (c *Client) SeedSavedAlbum(members []telegram.Message, groupedID int64, origin *telegram.ForwardOrigin, savedPeerID int64, savedPeerTitle, caption string) []telegram.Message {
	out := make([]telegram.Message, 0, len(members))
	for i, m := range members {
		m.GroupedID = groupedID
		m.Forward = origin
		m.SavedPeerID = savedPeerID
		m.SavedPeerTitle = savedPeerTitle
		if i == 0 {
			m.Caption = caption
		} else {
			m.Caption = ""
		}
		out = append(out, c.AddSavedMessage(m))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
