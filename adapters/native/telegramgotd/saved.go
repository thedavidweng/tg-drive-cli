package telegramgotd

import (
	"context"
	"io"
	"strings"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// Saved Messages support (td import saved). The saved chat is the self peer:
// it is not a channel, so it is addressed with InputPeerSelf and its messages
// are fetched and deleted through the plain messages.* methods rather than the
// channels.* ones.

// StreamSavedHistory walks the saved chat newest-first, covering the main
// chat and every Saved Messages 2.0 sub-chat in one read. Peer titles (the
// forward origin's channel title, the sub-chat's name) live only in each
// page's entity lists, so they are harvested per page and stitched onto the
// messages of that page and later ones.
func (c *Client) StreamSavedHistory(ctx context.Context, afterID int, fn func(tgtelegram.Message) error) (tgtelegram.HistoryMeta, error) {
	var meta tgtelegram.HistoryMeta
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, client *telegram.Client) error {
		selfID := savedSelfID(ctx, client)
		titles := map[int64]string{}
		var err error
		meta, err = c.paginateHistoryPages(ctx, api, &tg.InputPeerSelf{}, afterID, 0,
			func(page tg.MessagesMessagesClass) { harvestPeerTitles(page, titles) },
			func(msg *tg.Message) error {
				return fn(normalizeMainSavedPeer(withPeerTitles(messageFromTG(msg), titles), selfID))
			})
		return err
	})
	if err != nil {
		return tgtelegram.HistoryMeta{}, err
	}
	return meta, nil
}

func (c *Client) GetSavedMessage(ctx context.Context, messageID int) (tgtelegram.Message, error) {
	var out tgtelegram.Message
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, client *telegram.Client) error {
		msgs, err := api.MessagesGetMessages(ctx, []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}})
		if err != nil {
			return mapRPCError(err)
		}
		msg, err := firstMessage(msgs)
		if err != nil {
			return &tgtelegram.MessageNotFoundError{}
		}
		titles := map[int64]string{}
		harvestPeerTitles(msgs, titles)
		out = normalizeMainSavedPeer(withPeerTitles(messageFromTG(msg), titles), savedSelfID(ctx, client))
		return nil
	})
	return out, err
}

func (c *Client) DownloadSavedMedia(ctx context.Context, messageID int, dst io.Writer) error {
	return c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		msgs, err := api.MessagesGetMessages(ctx, []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}})
		if err != nil {
			return mapRPCError(err)
		}
		msg, err := firstMessage(msgs)
		if err != nil {
			return &tgtelegram.MessageNotFoundError{}
		}
		return streamMessageMedia(ctx, api, msg, dst)
	})
}

// DeleteSavedMessage deletes one saved message for the account. Revoke is set
// so the delete is not a local-only hide; Saved Messages has no tombstone
// form, so the removal is final.
func (c *Client) DeleteSavedMessage(ctx context.Context, messageID int) error {
	return c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		_, err := api.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{
			Revoke: true,
			ID:     []int{messageID},
		})
		return mapRPCError(err)
	})
}

// savedCapabilities probes what the saved chat allows: a one-message history
// read. Delete permission cannot be probed without destroying something, and
// Telegram always lets an account delete its own saved messages, so a
// readable saved chat implies a deletable one.
func (c *Client) savedCapabilities(ctx context.Context, api *tg.Client) (historyOK, deleteOK bool) {
	if _, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:  &tg.InputPeerSelf{},
		Limit: 1,
	}); err != nil {
		return false, false
	}
	return true, true
}

// harvestPeerTitles records the display title of every channel, chat, and user
// entity in one history page. Titles are snapshots: the origin channel of a
// saved message can be deleted, after which its name survives only in what
// was copied out here.
func harvestPeerTitles(msgs tg.MessagesMessagesClass, into map[int64]string) {
	var chats []tg.ChatClass
	var users []tg.UserClass
	switch v := msgs.(type) {
	case *tg.MessagesMessages:
		chats, users = v.Chats, v.Users
	case *tg.MessagesMessagesSlice:
		chats, users = v.Chats, v.Users
	case *tg.MessagesChannelMessages:
		chats, users = v.Chats, v.Users
	default:
		return
	}
	for _, ch := range chats {
		switch v := ch.(type) {
		case *tg.Channel:
			if v.Title != "" {
				into[v.ID] = v.Title
			}
		case *tg.Chat:
			if v.Title != "" {
				into[v.ID] = v.Title
			}
		}
	}
	for _, u := range users {
		v, ok := u.(*tg.User)
		if !ok {
			continue
		}
		name := strings.TrimSpace(strings.TrimSpace(v.FirstName) + " " + strings.TrimSpace(v.LastName))
		if name == "" {
			name = v.Username
		}
		if name != "" {
			into[v.ID] = name
		}
	}
}

// withPeerTitles fills the message's origin and sub-chat titles from the
// harvested entity titles. A title already carried by the forward header
// (from_name, set when the origin hid its peer) wins: it is the only name
// Telegram offers for that message.
func withPeerTitles(msg tgtelegram.Message, titles map[int64]string) tgtelegram.Message {
	if msg.Forward != nil && msg.Forward.Title == "" {
		msg.Forward.Title = titles[msg.Forward.FromID]
	}
	if msg.SavedPeerID != 0 {
		msg.SavedPeerTitle = titles[msg.SavedPeerID]
	}
	return msg
}

// savedSelfID returns the authenticated user's numeric id. Saved Messages
// stores directly-sent "My Notes" items with saved_peer_id=self, while the
// service contract uses zero for the main Saved Messages chat.
func savedSelfID(ctx context.Context, client *telegram.Client) int64 {
	self, err := client.Self(ctx)
	if err != nil || self == nil {
		return 0
	}
	return int64(self.ID)
}

func normalizeMainSavedPeer(msg tgtelegram.Message, selfID int64) tgtelegram.Message {
	if selfID != 0 && msg.Forward == nil && msg.SavedPeerID == selfID {
		msg.SavedPeerID = 0
		msg.SavedPeerTitle = ""
	}
	return msg
}
