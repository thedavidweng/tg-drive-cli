package fake

import (
	"context"
	"fmt"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// DiscussionClient implementation (ADR 0018). The fake models the linked
// discussion group as an ordinary fake channel: every channel post that
// receives its first comment lazily grows a forwarded header message, and
// comments are plain messages replying to that header.

func (c *Client) EnsureDiscussionGroup(ctx context.Context, channelID int64) (*telegram.Channel, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loggedIn {
		return nil, &telegram.AuthRequiredError{}
	}
	if c.denyPerms {
		return nil, &telegram.PermissionDeniedError{}
	}
	if gid := c.discussion[channelID]; gid != 0 {
		ch := *c.channels[gid]
		return &ch, nil
	}
	base, ok := c.channels[channelID]
	if !ok {
		return nil, &telegram.MessageNotFoundError{}
	}
	// Allocate like CreateChannel (pre-increment) so the group id can never
	// collide with the drive channel's id.
	c.nextChID++
	gid := c.nextChID
	group := &telegram.Channel{
		ID:         gid,
		Title:      base.Title + " Discussion",
		InviteLink: fmt.Sprintf("https://t.me/+fake%d", gid),
	}
	c.channels[gid] = group
	c.messages[gid] = nil
	c.discussion[channelID] = gid
	c.save()
	return group, nil
}

func (c *Client) LinkedDiscussionGroup(ctx context.Context, channelID int64) (*telegram.Channel, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	gid := c.discussion[channelID]
	if gid == 0 {
		return nil, false, nil
	}
	ch := *c.channels[gid]
	return &ch, true, nil
}

func (c *Client) SendThreadReply(ctx context.Context, channelID int64, postMsgID int, text string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.denyPerms {
		return 0, &telegram.PermissionDeniedError{}
	}
	gid := c.discussion[channelID]
	if gid == 0 {
		return 0, &telegram.DiscussionMissingError{}
	}
	if c.failReply {
		return 0, fmt.Errorf("simulated reply failure")
	}
	// Find or create the auto-forwarded header for this post.
	rootID := 0
	for root, post := range c.threadRoots {
		if post == int64(postMsgID) && c.inChannel(gid, int(root)) {
			rootID = int(root)
			break
		}
	}
	if rootID == 0 {
		rootID = c.nextID
		c.nextID++
		c.threadRoots[int64(rootID)] = int64(postMsgID)
		c.messages[gid] = append(c.messages[gid], telegram.Message{ID: rootID})
	}
	comment := telegram.Message{ID: c.nextID, Text: text, ReplyTo: &rootID}
	c.nextID++
	c.messages[gid] = append(c.messages[gid], comment)
	c.save()
	return comment.ID, nil
}

func (c *Client) EditThreadMessage(ctx context.Context, channelID int64, msgID int, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.denyPerms {
		return &telegram.PermissionDeniedError{}
	}
	gid := c.discussion[channelID]
	if gid == 0 {
		return &telegram.DiscussionMissingError{}
	}
	if c.failEditText {
		return fmt.Errorf("simulated edit failure")
	}
	for i, m := range c.messages[gid] {
		if m.ID == msgID {
			if m.NotEditable {
				return &telegram.MessageNotEditableError{}
			}
			c.messages[gid][i].Text = text
			c.save()
			return nil
		}
	}
	return &telegram.MessageNotFoundError{}
}

func (c *Client) DeleteThreadMessage(ctx context.Context, channelID int64, msgID int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.denyPerms {
		return &telegram.PermissionDeniedError{}
	}
	gid := c.discussion[channelID]
	if gid == 0 {
		return &telegram.DiscussionMissingError{}
	}
	c.deleteCalls++
	if c.failDelete || (c.failDeleteAfterOne && c.deleteCalls > 1) {
		return fmt.Errorf("delete failed")
	}
	for i, m := range c.messages[gid] {
		if m.ID == msgID {
			c.messages[gid] = append(c.messages[gid][:i], c.messages[gid][i+1:]...)
			delete(c.threadRoots, int64(msgID))
			c.save()
			return nil
		}
	}
	return &telegram.MessageNotFoundError{}
}

func (c *Client) StreamThreadHistory(ctx context.Context, channelID int64, afterID int, fn func(telegram.ThreadMessage) error) (telegram.HistoryMeta, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	gid := c.discussion[channelID]
	if gid == 0 {
		return telegram.HistoryMeta{}, &telegram.DiscussionMissingError{}
	}
	msgs, complete := c.historyLocked(gid, afterID, 0)
	meta := telegram.HistoryMeta{Complete: complete}
	for _, m := range msgs {
		tm := telegram.ThreadMessage{Message: m}
		if post, ok := c.threadRoots[int64(m.ID)]; ok {
			tm.RootMsgID = m.ID
			tm.PostID = int(post)
		}
		if err := fn(tm); err != nil {
			return telegram.HistoryMeta{}, err
		}
	}
	return meta, nil
}

// inChannel reports whether a message id lives in the given channel
// (callers hold c.mu).
func (c *Client) inChannel(channelID int64, msgID int) bool {
	for _, m := range c.messages[channelID] {
		if m.ID == msgID {
			return true
		}
	}
	return false
}
