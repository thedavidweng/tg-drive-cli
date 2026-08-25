package telegramgotd

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	tgtelegram "github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// DiscussionClient implementation (ADR 0018): the linked discussion
// supergroup carries every machine manifest record as a comment on the
// file's post thread.

type discussionPeer struct {
	id         int64
	accessHash int64
	title      string
}

// discussionGroup resolves the linked discussion group of a drive channel.
// ok is false when the channel has no linked group.
func (c *Client) discussionGroup(ctx context.Context, api *tg.Client, channelID int64) (discussionPeer, bool, error) {
	if p, ok := c.cachedDiscussion(channelID); ok {
		return p, true, nil
	}
	drive, err := c.resolveChannelPeer(ctx, api, strconv.FormatInt(channelID, 10))
	if err != nil {
		return discussionPeer{}, false, err
	}
	full, err := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: drive.ChannelID, AccessHash: drive.AccessHash})
	if err != nil {
		return discussionPeer{}, false, mapRPCError(err)
	}
	cf, ok := full.FullChat.(*tg.ChannelFull)
	if !ok || cf.LinkedChatID == 0 {
		return discussionPeer{}, false, nil
	}
	for _, chat := range full.Chats {
		ch, ok := chat.(*tg.Channel)
		if !ok || ch.ID != cf.LinkedChatID {
			continue
		}
		p := discussionPeer{id: ch.ID, accessHash: ch.AccessHash, title: ch.Title}
		c.cacheDiscussion(channelID, p)
		return p, true, nil
	}
	return discussionPeer{}, false, nil
}

func (c *Client) cachedDiscussion(channelID int64) (discussionPeer, bool) {
	c.discMu.Lock()
	defer c.discMu.Unlock()
	p, ok := c.discussion[channelID]
	return p, ok
}

func (c *Client) cacheDiscussion(channelID int64, p discussionPeer) {
	c.discMu.Lock()
	defer c.discMu.Unlock()
	c.discussion[channelID] = p
}

func (c *Client) EnsureDiscussionGroup(ctx context.Context, channelID int64) (*tgtelegram.Channel, error) {
	var out *tgtelegram.Channel
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		if p, ok, err := c.discussionGroup(ctx, api, channelID); err != nil {
			return err
		} else if ok {
			out = &tgtelegram.Channel{ID: p.id, AccessHash: p.accessHash, Title: p.title}
			return nil
		}
		drive, err := c.resolveChannelPeer(ctx, api, strconv.FormatInt(channelID, 10))
		if err != nil {
			return err
		}
		driveInput := &tg.InputChannel{ChannelID: drive.ChannelID, AccessHash: drive.AccessHash}
		title := "Discussion"
		if t, ok := c.channelTitleByID(channelID); ok {
			title = t + " Discussion"
		}
		upd, err := api.ChannelsCreateChannel(ctx, &tg.ChannelsCreateChannelRequest{
			Title: title, About: "tg-drive-cli machine records (ADR 0018)", Megagroup: true,
		})
		if err != nil {
			return mapRPCError(err)
		}
		updates, ok := upd.(*tg.Updates)
		if !ok {
			return fmt.Errorf("unexpected createChannel response %T", upd)
		}
		var group *tg.Channel
		for _, chat := range updates.Chats {
			if ch, ok := chat.(*tg.Channel); ok {
				group = ch
				break
			}
		}
		if group == nil {
			return fmt.Errorf("createChannel returned no channel")
		}
		groupInput := &tg.InputChannel{ChannelID: group.ID, AccessHash: group.AccessHash}
		if _, err := api.ChannelsTogglePreHistoryHidden(ctx, &tg.ChannelsTogglePreHistoryHiddenRequest{
			Channel: groupInput, Enabled: false,
		}); err != nil && !tgerr.Is(err, "CHAT_NOT_MODIFIED") {
			return mapRPCError(err)
		}
		if _, err := api.ChannelsSetDiscussionGroup(ctx, &tg.ChannelsSetDiscussionGroupRequest{
			Broadcast: driveInput, Group: groupInput,
		}); err != nil {
			return mapRPCError(err)
		}
		p := discussionPeer{id: group.ID, accessHash: group.AccessHash, title: group.Title}
		c.cacheDiscussion(channelID, p)
		c.rememberChannelTitle(group.ID, group.Title)
		out = &tgtelegram.Channel{ID: p.id, AccessHash: p.accessHash, Title: p.title}
		return nil
	})
	return out, err
}

func (c *Client) LinkedDiscussionGroup(ctx context.Context, channelID int64) (*tgtelegram.Channel, bool, error) {
	var out *tgtelegram.Channel
	ok := false
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		p, found, err := c.discussionGroup(ctx, api, channelID)
		if err != nil || !found {
			return err
		}
		out = &tgtelegram.Channel{ID: p.id, AccessHash: p.accessHash, Title: p.title}
		ok = true
		return nil
	})
	return out, ok, err
}

// threadRoot resolves the discussion-group message id of the auto-forwarded
// header for a channel post — the root of the post's comment thread. It also
// returns the discussion group peer taken from the same response.
func (c *Client) threadRoot(ctx context.Context, api *tg.Client, drive *tg.InputPeerChannel, postMsgID int) (int, tg.InputPeerClass, error) {
	res, err := api.MessagesGetDiscussionMessage(ctx, &tg.MessagesGetDiscussionMessageRequest{
		Peer: drive, MsgID: postMsgID,
	})
	if err != nil {
		return 0, nil, mapRPCError(err)
	}
	if len(res.Messages) == 0 {
		return 0, nil, &tgtelegram.MessageNotFoundError{}
	}
	// Reverse chronological: the LAST message is the forwarded header.
	msg, ok := res.Messages[len(res.Messages)-1].(*tg.Message)
	if !ok {
		return 0, nil, &tgtelegram.MessageNotFoundError{}
	}
	hdr, ok := msg.GetFwdFrom()
	if !ok || hdr.ChannelPost == 0 {
		return 0, nil, &tgtelegram.MessageNotFoundError{}
	}
	peerChannel, _ := msg.PeerID.(*tg.PeerChannel)
	for _, chat := range res.Chats {
		ch, ok := chat.(*tg.Channel)
		if !ok || !ch.Megagroup {
			continue
		}
		if peerChannel != nil && peerChannel.ChannelID == ch.ID {
			return msg.ID, channelPeer(ch.ID, ch.AccessHash), nil
		}
	}
	return msg.ID, nil, nil
}

func (c *Client) SendThreadReply(ctx context.Context, channelID int64, postMsgID int, text string) (int, error) {
	var id int
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		drive, err := c.resolveChannelPeer(ctx, api, strconv.FormatInt(channelID, 10))
		if err != nil {
			return err
		}
		p, ok, err := c.discussionGroup(ctx, api, channelID)
		if err != nil {
			return err
		}
		if !ok {
			return &tgtelegram.DiscussionMissingError{}
		}
		groupPeer := channelPeer(p.id, p.accessHash)
		rootID, _, err := c.threadRoot(ctx, api, drive, postMsgID)
		if err != nil {
			var nf *tgtelegram.MessageNotFoundError
			if !errors.As(err, &nf) {
				return err
			}
			// Posts published before the group was linked have no
			// auto-forwarded header. Bootstrapping: forwarding the post
			// into the group creates a header carrying
			// fwd_from.channel_post, which is the thread root everything
			// else (scan mapping included) already understands.
			fwd, ferr := api.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
				FromPeer: drive, ToPeer: groupPeer,
				ID: []int{postMsgID}, RandomID: []int64{randomID()},
			})
			if ferr != nil {
				return mapRPCError(ferr)
			}
			rootID, ferr = firstNewChannelMessageID(fwd)
			if ferr != nil {
				return ferr
			}
		}
		upd, err := api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
			Peer: groupPeer, Message: text, RandomID: randomID(),
			ReplyTo: &tg.InputReplyToMessage{ReplyToMsgID: rootID, TopMsgID: rootID},
		})
		if err != nil {
			return mapRPCError(err)
		}
		id, err = firstNewChannelMessageID(upd)
		return err
	})
	return id, err
}

func (c *Client) EditThreadMessage(ctx context.Context, channelID int64, msgID int, text string) error {
	return c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		p, ok, err := c.discussionGroup(ctx, api, channelID)
		if err != nil {
			return err
		}
		if !ok {
			return &tgtelegram.DiscussionMissingError{}
		}
		_, err = api.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{
			Peer: channelPeer(p.id, p.accessHash), ID: msgID, Message: text,
		})
		return mapRPCError(err)
	})
}

func (c *Client) DeleteThreadMessage(ctx context.Context, channelID int64, msgID int) error {
	return c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		p, ok, err := c.discussionGroup(ctx, api, channelID)
		if err != nil {
			return err
		}
		if !ok {
			return &tgtelegram.DiscussionMissingError{}
		}
		_, err = api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: p.id, AccessHash: p.accessHash},
			ID:      []int{msgID},
		})
		return mapRPCError(err)
	})
}

func (c *Client) StreamThreadHistory(ctx context.Context, channelID int64, afterID int, fn func(tgtelegram.ThreadMessage) error) (tgtelegram.HistoryMeta, error) {
	var meta tgtelegram.HistoryMeta
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		p, ok, err := c.discussionGroup(ctx, api, channelID)
		if err != nil {
			return err
		}
		if !ok {
			return &tgtelegram.DiscussionMissingError{}
		}
		peer := channelPeer(p.id, p.accessHash)
		offsetID := 0
		collected := 0
		prevMinID := 0
		for {
			msgs, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
				Peer: peer, Limit: historyPageSize, OffsetID: offsetID,
			})
			if err != nil {
				return mapRPCError(err)
			}
			// Completeness proof mirrors streamHistory: pagination is driven
			// by the RAW page including service messages.
			rawIDs := extractRawIDs(msgs)
			if len(rawIDs) == 0 {
				meta.Complete = true
				return nil
			}
			minID := rawIDs[0]
			for _, id := range rawIDs {
				if id < minID {
					minID = id
				}
			}
			if total := historyTotalCount(msgs); total > meta.TotalMessages {
				meta.TotalMessages = total
			}
			for _, msg := range extractMessages(msgs) {
				if msg.ID <= afterID {
					continue
				}
				if err := fn(c.threadMessageFromTG(msg)); err != nil {
					return err
				}
			}
			collected += len(rawIDs)
			if meta.OldestID == 0 || minID < meta.OldestID {
				meta.OldestID = minID
			}
			if meta.TotalMessages > 0 && afterID == 0 && collected >= meta.TotalMessages {
				meta.Complete = true
				return nil
			}
			if minID <= afterID+1 {
				meta.Complete = true
				return nil
			}
			if minID == prevMinID {
				meta.Complete = false
				return nil
			}
			prevMinID = minID
			offsetID = minID
		}
	})
	return meta, err
}

// threadMessageFromTG classifies one discussion-group message: forwarded
// channel post headers become roots, everything else a comment/chatter.
func (c *Client) threadMessageFromTG(msg *tg.Message) tgtelegram.ThreadMessage {
	if hdr, ok := msg.GetFwdFrom(); ok && hdr.ChannelPost != 0 {
		return tgtelegram.ThreadMessage{
			Message:   tgtelegram.Message{ID: msg.ID},
			RootMsgID: msg.ID,
			PostID:    int(hdr.ChannelPost),
		}
	}
	return tgtelegram.ThreadMessage{Message: messageFromTG(msg)}
}

func (c *Client) channelTitleByID(id int64) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.channelTitles[id]
	return t, ok
}

func firstNewChannelMessageID(upd tg.UpdatesClass) (int, error) {
	updates, ok := upd.(*tg.Updates)
	if !ok {
		return 0, fmt.Errorf("unexpected updates type %T", upd)
	}
	for _, u := range updates.Updates {
		if uncm, ok := u.(*tg.UpdateNewChannelMessage); ok {
			if msg, ok := uncm.Message.(*tg.Message); ok {
				return msg.ID, nil
			}
		}
	}
	return 0, fmt.Errorf("no new channel message in updates")
}

func randomID() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int64(binary.LittleEndian.Uint64(b[:]) & 0x7fffffffffffffff)
}
