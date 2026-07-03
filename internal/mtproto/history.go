package mtproto

import (
	"context"
	"strconv"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/internal/telegram"
)

func (c *Client) History(ctx context.Context, channelID int64, afterID int, limit int) ([]tgtelegram.Message, error) {
	var out []tgtelegram.Message
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		peer, err := c.resolveChannelPeer(ctx, api, strconv.FormatInt(channelID, 10))
		if err != nil {
			return err
		}
		offsetID := 0
		const pageSize = 100
		for limit <= 0 || len(out) < limit {
			batch := pageSize
			if limit > 0 && limit-len(out) < batch {
				batch = limit - len(out)
			}
			msgs, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
				Peer:     peer,
				Limit:    batch,
				OffsetID: offsetID,
			})
			if err != nil {
				return mapRPCError(err)
			}
			batchMsgs := extractMessages(msgs)
			if len(batchMsgs) == 0 {
				break
			}
			for _, msg := range batchMsgs {
				if msg.ID <= afterID {
					continue
				}
				out = append(out, messageFromTG(msg))
			}
			if len(batchMsgs) < batch {
				break
			}
			offsetID = batchMsgs[len(batchMsgs)-1].ID
		}
		return nil
	})
	return out, err
}

func extractMessages(msgs tg.MessagesMessagesClass) []*tg.Message {
	var list []tg.MessageClass
	switch v := msgs.(type) {
	case *tg.MessagesMessages:
		list = v.Messages
	case *tg.MessagesMessagesSlice:
		list = v.Messages
	case *tg.MessagesChannelMessages:
		list = v.Messages
	default:
		return nil
	}
	var out []*tg.Message
	for _, m := range list {
		if msg, ok := m.(*tg.Message); ok {
			out = append(out, msg)
		}
	}
	return out
}

func messageFromTG(msg *tg.Message) tgtelegram.Message {
	out := tgtelegram.Message{
		ID:      msg.ID,
		Caption: msg.Message,
	}
	if msg.Media != nil {
		if media, ok := msg.Media.(*tg.MessageMediaDocument); ok {
			if doc, ok := media.Document.(*tg.Document); ok {
				out.FileSize = doc.Size
				out.MIME = doc.MimeType
				for _, attr := range doc.Attributes {
					if fn, ok := attr.(*tg.DocumentAttributeFilename); ok {
						out.FileName = fn.FileName
					}
				}
			}
		}
	}
	if msg.ReplyTo != nil {
		if rt, ok := msg.ReplyTo.(*tg.MessageReplyHeader); ok {
			id := rt.ReplyToMsgID
			out.ReplyTo = &id
		}
	}
	return out
}

func (c *Client) Doctor(ctx context.Context, channelID int64) (*tgtelegram.Capabilities, error) {
	var caps *tgtelegram.Capabilities
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, client *telegram.Client) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return mapRPCError(err)
		}
		caps = &tgtelegram.Capabilities{
			AuthOK:         status.Authorized,
			MaxUploadBytes: 2147483648,
			CheckedAt:      time.Now().UTC(),
		}
		if !status.Authorized {
			return nil
		}
		peer, err := c.resolveChannelPeer(ctx, api, strconv.FormatInt(channelID, 10))
		if err != nil {
			caps.ChannelOK = false
			return nil
		}
		caps.ChannelOK = true
		chats, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
			&tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash},
		})
		if err != nil {
			return mapRPCError(err)
		}
		ch, err := firstChannel(chats)
		if err != nil {
			caps.ChannelOK = false
			return nil
		}
		if ch.Creator {
			caps.UploadOK = true
			caps.DeleteOK = true
			caps.EditOldCaptionOK = true
		} else {
			caps.UploadOK = ch.AdminRights.PostMessages
			caps.DeleteOK = ch.AdminRights.DeleteMessages
			caps.EditOldCaptionOK = ch.AdminRights.EditMessages
		}
		if _, err := c.exportInvite(ctx, api, ch); err == nil {
			caps.InviteLinkOK = true
		}
		self, err := client.Self(ctx)
		if err == nil && self.Premium {
			caps.MaxUploadBytes = 4294967296
		}
		return nil
	})
	return caps, err
}
