package telegramgotd

import (
	"context"
	"strconv"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/core/telegram"
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
			// Pagination must be driven by the RAW page (including service
			// messages the media filter drops), or scans stop early.
			rawIDs := extractRawIDs(msgs)
			if len(rawIDs) == 0 {
				break
			}
			minID := rawIDs[0]
			for _, id := range rawIDs {
				if id < minID {
					minID = id
				}
			}
			for _, msg := range extractMessages(msgs) {
				if msg.ID <= afterID {
					continue
				}
				out = append(out, messageFromTG(msg))
			}
			if len(rawIDs) < batch || minID <= afterID+1 {
				break
			}
			offsetID = minID
		}
		return nil
	})
	return out, err
}

// extractRawIDs returns IDs of every message in the page, including service
// and empty messages, for pagination bookkeeping.
func extractRawIDs(msgs tg.MessagesMessagesClass) []int {
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
	out := make([]int, 0, len(list))
	for _, m := range list {
		switch v := m.(type) {
		case *tg.Message:
			out = append(out, v.ID)
		case *tg.MessageService:
			out = append(out, v.ID)
		case *tg.MessageEmpty:
			out = append(out, v.ID)
		}
	}
	return out
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
	out := tgtelegram.Message{ID: msg.ID}
	// Media messages carry their body as Caption; plain text messages
	// (e.g. td-manifest:v1 replies) carry it as Text. Scan depends on this
	// distinction to route manifest replies through the manifest parser.
	if msg.Media == nil {
		out.Text = msg.Message
	} else {
		out.Caption = msg.Message
	}
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
