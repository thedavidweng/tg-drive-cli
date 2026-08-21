package telegramgotd

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/core/telegram"
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
		c.RegisterChannelInfo(ch.ID, ch.AccessHash, ch.Title)
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
		c.RegisterChannelInfo(ch.ID, ch.AccessHash, ch.Title)
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

const inviteCacheTTL = 5 * time.Minute

func (c *Client) GetInviteLink(ctx context.Context, channelID int64) (string, error) {
	c.inviteMu.Lock()
	if e, ok := c.inviteCache[channelID]; ok && e.expiry.After(time.Now()) {
		c.inviteMu.Unlock()
		return e.link, nil
	}
	c.inviteMu.Unlock()

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
		c.RegisterChannelInfo(ch.ID, ch.AccessHash, ch.Title)
		l, err := c.exportInvite(ctx, api, ch)
		if err != nil {
			return err
		}
		link = l
		return nil
	})
	if err == nil && link != "" {
		c.inviteMu.Lock()
		c.inviteCache[channelID] = cachedInvite{link: link, expiry: time.Now().Add(inviteCacheTTL)}
		c.inviteMu.Unlock()
	}
	return link, err
}

func (c *Client) exportInvite(ctx context.Context, api *tg.Client, ch *tg.Channel) (string, error) {
	// Public channels are accessed by username.
	if ch.Username != "" {
		return "https://t.me/" + ch.Username, nil
	}

	// Try to read the existing primary invite without minting a new one.
	full, err := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash})
	if err != nil {
		return "", mapRPCError(err)
	}
	if chFull, ok := full.FullChat.(*tg.ChannelFull); ok {
		if inv, ok := chFull.GetExportedInvite(); ok {
			if exp, ok := inv.(*tg.ChatInviteExported); ok && exp.Link != "" {
				return exp.Link, nil
			}
		}
	}

	// No existing invite, fall back to creating one.
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

type uploadProgress struct {
	cb tgtelegram.UploadProgress
}

func (p *uploadProgress) Chunk(ctx context.Context, state uploader.ProgressState) error {
	if p.cb == nil {
		return nil
	}
	return p.cb(ctx, tgtelegram.UploadProgressState{
		FileName: state.Name,
		Part:     state.Part,
		PartSize: state.PartSize,
		Uploaded: state.Uploaded,
		Total:    state.Total,
	})
}

func (c *Client) UploadMedia(ctx context.Context, req tgtelegram.UploadRequest) (*tgtelegram.UploadResult, error) {
	var result *tgtelegram.UploadResult
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		peer, err := c.resolveChannelPeer(ctx, api, strconv.FormatInt(req.ChannelID, 10))
		if err != nil {
			return err
		}

		upl := selectMediaUploader(req)
		uploaded, err := upl.upload(ctx, api, req)
		if err != nil {
			return mapRPCError(err)
		}

		// Message-creating RPCs are never blindly retried here: a send that
		// may have succeeded server-side must not be re-issued (that would
		// duplicate the media message). Flood-wait pacing and idempotent
		// connection-level retries live in the rate-limiter middleware; the
		// duplicate-claim guard during scan remains the reconciliation path.
		sender := message.NewSender(api)
		var updates tg.UpdatesClass
		if req.Kind == tgtelegram.KindPhoto {
			// Native photo message: full preview UX; Telegram recompresses
			// the bytes server-side.
			updates, err = sender.To(peer).Media(ctx,
				message.UploadedPhoto(uploaded, styling.Plain(req.Caption)))
		} else {
			var media message.MediaOption
			media, buildErr := c.documentMedia(ctx, api, uploaded, req, req.Kind == tgtelegram.KindVideo)
			if buildErr != nil {
				return buildErr
			}
			updates, err = sender.To(peer).Media(ctx, media)
		}
		if err != nil {
			return mapRPCError(err)
		}
		msgID, extractErr := extractMessageID(updates)
		if extractErr != nil {
			return extractErr
		}
		result = &tgtelegram.UploadResult{MessageID: msgID}
		return nil
	})
	return result, err
}

// documentMedia builds the uploaded-document media option for document and
// video kinds. Video adds a video attribute block (streaming enabled when
// requested); both attach the optional upload thumbnail.
func (c *Client) documentMedia(ctx context.Context, api *tg.Client, uploaded tg.InputFileClass, req tgtelegram.UploadRequest, video bool) (message.MediaOption, error) {
	doc := message.UploadedDocument(uploaded, styling.Plain(req.Caption)).Filename(req.FileName)
	if req.MIME != "" {
		doc = doc.MIME(req.MIME)
	}
	if len(req.Thumb) > 0 {
		thumbFile, err := uploadThumbnail(ctx, api, req.Thumb)
		if err != nil {
			return nil, mapRPCError(err)
		}
		doc = doc.Thumb(thumbFile)
	}
	if video {
		attr := &tg.DocumentAttributeVideo{}
		if req.Video != nil {
			attr.Duration = req.Video.DurationSeconds
			attr.W = req.Video.Width
			attr.H = req.Video.Height
			attr.SupportsStreaming = req.Video.SupportsStreaming
		}
		doc = doc.Attributes(attr)
	}
	return doc, nil
}

func extractMessageID(updates tg.UpdatesClass) (int, error) {
	var list []tg.UpdateClass
	switch u := updates.(type) {
	case *tg.Updates:
		list = u.Updates
	case *tg.UpdatesCombined:
		list = u.Updates
	case *tg.UpdateShortSentMessage:
		return u.ID, nil
	default:
		return 0, errors.New("message id not found")
	}
	for _, upd := range list {
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
		// SetMessage sets the TL flag even when caption is empty, which is
		// how album sibling captions are cleared. Assigning Message: ""
		// alone leaves the flag unset and Telegram treats it as "no edit".
		req := &tg.MessagesEditMessageRequest{
			Peer: peer,
			ID:   messageID,
		}
		req.SetMessage(caption)
		_, err = api.MessagesEditMessage(ctx, req)
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
		switch media := msg.Media.(type) {
		case *tg.MessageMediaDocument:
			if media.Document == nil {
				return errors.New("message has no document")
			}
			doc, ok := media.Document.(*tg.Document)
			if !ok {
				return errors.New("unsupported document type")
			}
			dl := downloader.NewDownloader()
			_, err = dl.Download(api, doc.AsInputDocumentFileLocation("")).Stream(ctx, dst)
			return mapRPCError(err)
		case *tg.MessageMediaPhoto:
			photo, ok := media.Photo.(*tg.Photo)
			if !ok {
				return errors.New("message has no photo")
			}
			thumb := largestPhotoType(photo)
			dl := downloader.NewDownloader()
			_, err = dl.Download(api, photo.AsInputPhotoFileLocation(thumb)).Stream(ctx, dst)
			return mapRPCError(err)
		case nil:
			if strings.TrimSpace(msg.Message) == "" {
				return errors.New("message has no downloadable content")
			}
			_, err := dst.Write([]byte(manifest.SplitHumanAndMachine(msg.Message)))
			return err
		default:
			return errors.New("unsupported media type")
		}
	})
}

func largestPhotoType(photo *tg.Photo) string {
	bestType := "y"
	best := 0
	for _, s := range photo.Sizes {
		switch v := s.(type) {
		case *tg.PhotoSize:
			if v.W*v.H > best {
				best = v.W * v.H
				bestType = v.Type
			}
		case *tg.PhotoSizeProgressive:
			if v.W*v.H > best {
				best = v.W * v.H
				bestType = v.Type
			}
		}
	}
	return bestType
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
