package mtproto

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"io"
	"strconv"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/internal/telegram"
)

func (c *Client) CreateChannel(ctx context.Context, title string) (*tgtelegram.Channel, error) {
	var out *tgtelegram.Channel
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		channelTitle := formatTDChannelTitle(title)
		updates, err := api.ChannelsCreateChannel(ctx, &tg.ChannelsCreateChannelRequest{
			Title:     channelTitle,
			About:     tdChannelAbout,
			Broadcast: true,
		})
		if err != nil {
			return mapRPCError(err)
		}
		ch, err := extractChannel(updates)
		if err != nil {
			return err
		}
		c.rememberChannel(ch.ID, ch.AccessHash)
		_, _ = api.MessagesSetHistoryTTL(ctx, &tg.MessagesSetHistoryTTLRequest{
			Peer:   channelPeer(ch.ID, ch.AccessHash),
			Period: 0,
		})
		link, _ := c.exportInvite(ctx, api, ch)
		out = &tgtelegram.Channel{
			ID:         ch.ID,
			AccessHash: ch.AccessHash,
			Title:      channelTitle,
			Username:   ch.Username,
			InviteLink: link,
		}
		return nil
	})
	return out, err
}

func (c *Client) ResolveChannel(ctx context.Context, titleOrID string) (*tgtelegram.Channel, error) {
	var out *tgtelegram.Channel
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		peer, err := c.resolveChannelPeer(ctx, api, titleOrID)
		if err != nil {
			return err
		}
		chats, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
			&tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash},
		})
		if err != nil {
			return mapRPCError(err)
		}
		ch, err := firstChannel(chats)
		if err != nil {
			return err
		}
		c.rememberChannel(ch.ID, ch.AccessHash)
		link, _ := c.exportInvite(ctx, api, ch)
		out = &tgtelegram.Channel{
			ID:         ch.ID,
			AccessHash: ch.AccessHash,
			Title:      ch.Title,
			Username:   ch.Username,
			InviteLink: link,
		}
		return nil
	})
	return out, err
}

func (c *Client) BindChannel(ctx context.Context, titleOrID string) (*tgtelegram.Channel, error) {
	return c.ResolveChannel(ctx, titleOrID)
}

func (c *Client) GetInviteLink(ctx context.Context, channelID int64) (string, error) {
	var link string
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		peer, err := c.resolveChannelPeer(ctx, api, strconv.FormatInt(channelID, 10))
		if err != nil {
			return err
		}
		chats, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
			&tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash},
		})
		if err != nil {
			return mapRPCError(err)
		}
		ch, err := firstChannel(chats)
		if err != nil {
			return err
		}
		l, err := c.exportInvite(ctx, api, ch)
		if err != nil {
			return err
		}
		link = l
		return nil
	})
	return link, err
}

func (c *Client) exportInvite(ctx context.Context, api *tg.Client, ch *tg.Channel) (string, error) {
	exported, err := api.MessagesExportChatInvite(ctx, &tg.MessagesExportChatInviteRequest{
		Peer: channelPeer(ch.ID, ch.AccessHash),
	})
	if err != nil {
		return "", mapRPCError(err)
	}
	if inv, ok := exported.(*tg.ChatInviteExported); ok {
		return inv.Link, nil
	}
	return "", nil
}

func extractChannel(updates tg.UpdatesClass) (*tg.Channel, error) {
	var chats []tg.ChatClass
	switch u := updates.(type) {
	case *tg.Updates:
		chats = u.Chats
	case *tg.UpdatesCombined:
		chats = u.Chats
	default:
		return nil, errors.New("channel not found in updates")
	}
	for _, chat := range chats {
		if ch, ok := chat.(*tg.Channel); ok {
			return ch, nil
		}
	}
	return nil, errors.New("channel not found in updates")
}

func firstChannel(chats tg.MessagesChatsClass) (*tg.Channel, error) {
	var list []tg.ChatClass
	switch v := chats.(type) {
	case *tg.MessagesChats:
		list = v.Chats
	case *tg.MessagesChatsSlice:
		list = v.Chats
	default:
		return nil, errors.New("channel not found")
	}
	for _, ch := range list {
		if c, ok := ch.(*tg.Channel); ok {
			return c, nil
		}
	}
	return nil, errors.New("channel not found")
}

func (c *Client) UploadMedia(ctx context.Context, req tgtelegram.UploadRequest) (*tgtelegram.UploadResult, error) {
	var result *tgtelegram.UploadResult
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		peer, err := c.resolveChannelPeer(ctx, api, strconv.FormatInt(req.ChannelID, 10))
		if err != nil {
			return err
		}
		up := uploader.NewUploader(api)
		sender := message.NewSender(api).WithUploader(up)
		uploaded, err := up.Upload(ctx, uploader.NewUpload(req.FileName, req.Reader, req.Size))
		if err != nil {
			return mapRPCError(err)
		}
		doc := message.UploadedDocument(uploaded, styling.Plain(req.Caption)).Filename(req.FileName)
		if req.MIME != "" {
			doc = doc.MIME(req.MIME)
		}

		const maxRetries = 3
		var lastErr error
		for attempt := 0; attempt <= maxRetries; attempt++ {
			updates, err := sender.To(peer).Media(ctx, doc)
			if err == nil {
				msgID, extractErr := extractMessageID(updates)
				if extractErr != nil {
					return extractErr
				}
				result = &tgtelegram.UploadResult{MessageID: msgID}
				return nil
			}
			lastErr = mapRPCError(err)
			if fw, ok := lastErr.(*tgtelegram.FloodWaitError); ok && c.waitFlood {
				time.Sleep(time.Duration(fw.Seconds) * time.Second)
				continue
			}
			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
				continue
			}
			return lastErr
		}
		return lastErr
	})
	return result, err
}

func extractMessageID(updates tg.UpdatesClass) (int, error) {
	switch u := updates.(type) {
	case *tg.Updates:
		for _, upd := range u.Updates {
			if m, ok := upd.(*tg.UpdateNewChannelMessage); ok {
				if msg, ok := m.Message.(*tg.Message); ok {
					return msg.ID, nil
				}
			}
			if m, ok := upd.(*tg.UpdateNewMessage); ok {
				if msg, ok := m.Message.(*tg.Message); ok {
					return msg.ID, nil
				}
			}
		}
	case *tg.UpdateShortSentMessage:
		return u.ID, nil
	}
	return 0, errors.New("message id not found")
}

func (c *Client) SendTextReply(ctx context.Context, channelID int64, replyTo int, text string) (int, error) {
	var msgID int
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		peer, err := c.resolveChannelPeer(ctx, api, strconv.FormatInt(channelID, 10))
		if err != nil {
			return err
		}
		updates, err := api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
			Peer:     peer,
			Message:  text,
			RandomID: randInt64(),
			ReplyTo: &tg.InputReplyToMessage{
				ReplyToMsgID: replyTo,
			},
		})
		if err != nil {
			return mapRPCError(err)
		}
		id, err := extractMessageID(updates)
		if err != nil {
			return err
		}
		msgID = id
		return nil
	})
	return msgID, err
}

func (c *Client) EditCaption(ctx context.Context, channelID int64, messageID int, caption string) error {
	return c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		peer, err := c.resolveChannelPeer(ctx, api, strconv.FormatInt(channelID, 10))
		if err != nil {
			return err
		}
		_, err = api.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{
			Peer:    peer,
			ID:      messageID,
			Message: caption,
		})
		return mapRPCError(err)
	})
}

func (c *Client) EditText(ctx context.Context, channelID int64, messageID int, text string) error {
	return c.EditCaption(ctx, channelID, messageID, text)
}

func (c *Client) DeleteMessage(ctx context.Context, channelID int64, messageID int) error {
	return c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		peer, err := c.resolveChannelPeer(ctx, api, strconv.FormatInt(channelID, 10))
		if err != nil {
			return err
		}
		_, err = api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash},
			ID:      []int{messageID},
		})
		return mapRPCError(err)
	})
}

func (c *Client) DownloadMedia(ctx context.Context, channelID int64, messageID int, dst io.Writer) error {
	return c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		peer, err := c.resolveChannelPeer(ctx, api, strconv.FormatInt(channelID, 10))
		if err != nil {
			return err
		}
		msgs, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash},
			ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}},
		})
		if err != nil {
			return mapRPCError(err)
		}
		msg, err := firstMessage(msgs)
		if err != nil {
			return err
		}
		media, ok := msg.Media.(*tg.MessageMediaDocument)
		if !ok || media.Document == nil {
			return errors.New("message has no document")
		}
		doc, ok := media.Document.(*tg.Document)
		if !ok {
			return errors.New("unsupported document type")
		}
		dl := downloader.NewDownloader()
		_, err = dl.Download(api, doc.AsInputDocumentFileLocation("")).WithVerify(true).Stream(ctx, dst)
		return mapRPCError(err)
	})
}

func firstMessage(msgs tg.MessagesMessagesClass) (*tg.Message, error) {
	var list []tg.MessageClass
	switch v := msgs.(type) {
	case *tg.MessagesChannelMessages:
		list = v.Messages
	case *tg.MessagesMessages:
		list = v.Messages
	default:
		return nil, errors.New("message not found")
	}
	for _, m := range list {
		if msg, ok := m.(*tg.Message); ok {
			return msg, nil
		}
	}
	return nil, errors.New("message not found")
}

func randInt64() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int64(binary.LittleEndian.Uint64(b[:]))
}
