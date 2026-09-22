package telegramgotd

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/core/telegram"
)

const historyPageSize = 100

// History returns messages newer than afterID, newest-first.
func (c *Client) History(ctx context.Context, channelID int64, afterID, limit int) ([]tgtelegram.Message, error) {
	var out []tgtelegram.Message
	meta, err := c.streamHistory(ctx, channelID, afterID, limit, func(msg tgtelegram.Message) error {
		out = append(out, msg)
		return nil
	})
	if err != nil {
		return nil, err
	}
	_ = meta
	return out, nil
}

// StreamHistory feeds messages newer than afterID to fn, newest-first, page by
// page, without materializing the channel.
func (c *Client) StreamHistory(ctx context.Context, channelID int64, afterID int, fn func(tgtelegram.Message) error) (tgtelegram.HistoryMeta, error) {
	return c.streamHistory(ctx, channelID, afterID, 0, fn)
}

// streamHistory paginates messages.getHistory from newest to oldest.
func (c *Client) streamHistory(ctx context.Context, channelID int64, afterID, limit int, fn func(tgtelegram.Message) error) (tgtelegram.HistoryMeta, error) {
	var meta tgtelegram.HistoryMeta
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		peer, err := c.resolveChannelPeer(ctx, api, strconv.FormatInt(channelID, 10))
		if err != nil {
			return err
		}
		meta, err = c.paginateHistory(ctx, api, peer, afterID, limit, func(msg *tg.Message) error {
			return fn(messageFromTG(msg))
		})
		return err
	})
	if err != nil {
		return tgtelegram.HistoryMeta{}, err
	}
	return meta, nil
}

// paginateHistory walks messages.getHistory for one peer from newest to
// oldest, feeding every raw message newer than afterID to emit. limit <= 0
// walks to the end of history. A short page is never trusted as the end of
// history by itself: pagination only reports Complete when it saw an empty
// page (the true end), crossed the afterID boundary, or collected the peer's
// reported total. A Telegram pagination quirk that returns a short page
// mid-history therefore surfaces as Complete=false instead of silently
// truncating the read.
func (c *Client) paginateHistory(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, afterID, limit int, emit func(*tg.Message) error) (tgtelegram.HistoryMeta, error) {
	return c.paginateHistoryPages(ctx, api, peer, afterID, limit, nil, emit)
}

// paginateHistoryPages is paginateHistory with an optional per-page hook that
// runs before the page's messages are emitted. Saved-chat reads use it to
// harvest the page's peer entities (channel and chat titles), which are the
// only place forward-origin and sub-chat names appear.
func (c *Client) paginateHistoryPages(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, afterID, limit int, onPage func(tg.MessagesMessagesClass), emit func(*tg.Message) error) (tgtelegram.HistoryMeta, error) {
	var meta tgtelegram.HistoryMeta
	offsetID := 0
	collected := 0
	prevMinID := 0
	for limit <= 0 || collected < limit {
		batch := historyPageSize
		if limit > 0 && limit-collected < batch {
			batch = limit - collected
		}
		msgs, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:     peer,
			Limit:    batch,
			OffsetID: offsetID,
		})
		if err != nil {
			return tgtelegram.HistoryMeta{}, mapRPCError(err)
		}
		if onPage != nil {
			onPage(msgs)
		}
		// Pagination must be driven by the RAW page (including service
		// messages the media filter drops), or scans stop early.
		rawIDs := extractRawIDs(msgs)
		if len(rawIDs) == 0 {
			meta.Complete = true
			return meta, nil
		}
		minID := rawIDs[0]
		for _, id := range rawIDs {
			if id < minID {
				minID = id
			}
		}
		if total := historyTotalCount(msgs); total > meta.TotalMessages {
			// Only a total that never decreased counts as proof:
			// mid-scan deletions above the read position shrink Count
			// while collected only grows, which would fake completion.
			meta.TotalMessages = total
		}
		for _, msg := range extractMessages(msgs) {
			if msg.ID <= afterID {
				continue
			}
			if err := emit(msg); err != nil {
				return tgtelegram.HistoryMeta{}, err
			}
		}
		collected += len(rawIDs)
		if meta.OldestID == 0 || minID < meta.OldestID {
			meta.OldestID = minID
		}
		if meta.TotalMessages > 0 && afterID == 0 && collected >= meta.TotalMessages {
			meta.Complete = true
			return meta, nil
		}
		if minID <= afterID+1 {
			meta.Complete = true
			return meta, nil
		}
		if minID == prevMinID {
			// The server returned the same page twice; without progress
			// we cannot prove the read covered the history below.
			meta.Complete = false
			return meta, nil
		}
		prevMinID = minID
		offsetID = minID
	}
	// The loop exited because the caller's limit was reached, not because
	// completion was proven.
	meta.Complete = false
	return meta, nil
}

// historyTotalCount returns the total message count Telegram reports for the
// channel, when the response carries one.
func historyTotalCount(msgs tg.MessagesMessagesClass) int {
	switch v := msgs.(type) {
	case *tg.MessagesChannelMessages:
		return v.Count
	case *tg.MessagesMessagesSlice:
		return v.Count
	default:
		return 0
	}
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
		if strings.TrimSpace(msg.Message) != "" {
			out.Kind = tgtelegram.KindText
			out.MIME = "text/plain"
			out.FileSize = int64(len(msg.Message))
		}
	} else {
		out.Caption = msg.Message
	}
	switch media := msg.Media.(type) {
	case *tg.MessageMediaDocument:
		if doc, ok := media.Document.(*tg.Document); ok {
			out.Kind = tgtelegram.KindDocument
			out.FileSize = doc.Size
			out.MIME = doc.MimeType
			for _, attr := range doc.Attributes {
				switch a := attr.(type) {
				case *tg.DocumentAttributeFilename:
					out.FileName = a.FileName
				case *tg.DocumentAttributeVideo:
					out.Video = &tgtelegram.VideoAttributes{
						DurationSeconds:   a.Duration,
						Width:             a.W,
						Height:            a.H,
						SupportsStreaming: a.SupportsStreaming,
					}
				}
			}
		}
	case *tg.MessageMediaPhoto:
		if _, ok := media.Photo.(*tg.Photo); ok {
			out.Kind = tgtelegram.KindPhoto
			out.MIME = "image/jpeg"
		}
	}
	if msg.ReplyTo != nil {
		if rt, ok := msg.ReplyTo.(*tg.MessageReplyHeader); ok {
			id := rt.ReplyToMsgID
			out.ReplyTo = &id
		}
	}
	if id, ok := msg.GetGroupedID(); ok {
		out.GroupedID = id
	}
	out.Date = unixTime(msg.Date)
	if fwd, ok := msg.GetFwdFrom(); ok {
		origin := &tgtelegram.ForwardOrigin{Date: unixTime(fwd.Date)}
		if from, ok := fwd.GetFromID(); ok {
			origin.FromID = peerRawID(from)
		}
		if name, ok := fwd.GetFromName(); ok {
			origin.Title = name
		}
		if post, ok := fwd.GetChannelPost(); ok {
			origin.PostID = post
		}
		out.Forward = origin
	}
	if saved, ok := msg.GetSavedPeerID(); ok {
		out.SavedPeerID = peerRawID(saved)
	}
	return out
}

// unixTime converts a Telegram second-resolution timestamp; 0 stays zero so
// callers can tell "unknown" from "the epoch".
func unixTime(sec int) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(sec), 0).UTC()
}

// peerRawID flattens a peer reference to its bare numeric id. Callers only
// need identity and a title snapshot, never a re-resolvable peer handle: the
// origin of a saved message may be gone by the time anyone reads the record.
func peerRawID(p tg.PeerClass) int64 {
	switch v := p.(type) {
	case *tg.PeerChannel:
		return v.ChannelID
	case *tg.PeerUser:
		return v.UserID
	case *tg.PeerChat:
		return v.ChatID
	default:
		return 0
	}
}

func (c *Client) GetMessage(ctx context.Context, channelID int64, messageID int) (tgtelegram.Message, error) {
	var out tgtelegram.Message
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
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
			return &tgtelegram.MessageNotFoundError{}
		}
		out = messageFromTG(msg)
		return nil
	})
	return out, err
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
		caps.SavedHistoryOK, caps.SavedDeleteOK = c.savedCapabilities(ctx, api)
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
		c.RegisterChannelInfo(ch.ID, ch.AccessHash, ch.Title)
		if ch.Creator {
			caps.UploadOK = true
			caps.DeleteOK = true
			caps.EditOldCaptionOK = true
		} else {
			caps.UploadOK = ch.AdminRights.PostMessages
			caps.DeleteOK = ch.AdminRights.DeleteMessages
			caps.EditOldCaptionOK = ch.AdminRights.EditMessages
		}
		if _, ok, err := c.discussionGroup(ctx, api, channelID); err == nil && ok {
			caps.DiscussionOK = true
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
