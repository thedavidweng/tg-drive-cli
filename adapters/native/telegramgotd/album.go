package telegramgotd

import (
	"context"
	"sort"
	"strconv"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// UploadMediaGroup sends the requests as one native media group via
// messages.sendMultiMedia. Only the first member's caption is honored;
// Telegram assigns one shared grouped id to the whole group.
func (c *Client) UploadMediaGroup(ctx context.Context, reqs []tgtelegram.UploadRequest) ([]tgtelegram.UploadResult, error) {
	if len(reqs) == 0 || len(reqs) > tgtelegram.MaxMediaGroupMembers {
		return nil, errors.Errorf("media group holds 1..%d members, got %d", tgtelegram.MaxMediaGroupMembers, len(reqs))
	}
	var results []tgtelegram.UploadResult
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		peer, err := c.resolveChannelPeer(ctx, api, strconv.FormatInt(reqs[0].ChannelID, 10))
		if err != nil {
			return err
		}

		media := make([]tg.InputSingleMedia, 0, len(reqs))
		for i, req := range reqs {
			upl := selectMediaUploader(req)
			uploaded, err := upl.upload(ctx, api, req)
			if err != nil {
				return mapRPCError(err)
			}
			var thumbFile tg.InputFileClass
			if len(req.Thumb) > 0 {
				if thumbFile, err = uploadThumbnail(ctx, api, req.Thumb); err != nil {
					return mapRPCError(err)
				}
			}
			input := buildAlbumInputMedia(uploaded, thumbFile, req)
			single := tg.InputSingleMedia{Media: input, RandomID: randInt64()}
			if i == 0 {
				// sendMultiMedia carries captions on the wrapper, not the
				// media; sibling messages stay empty per ADR 0013.
				single.Message = req.Caption
			}
			media = append(media, single)
		}

		// Message-creating RPCs are never blindly retried here (see
		// UploadMedia): a send that may have succeeded server-side must not be
		// re-issued. Flood-wait pacing lives in the rate-limiter middleware.
		updates, err := api.MessagesSendMultiMedia(ctx, &tg.MessagesSendMultiMediaRequest{
			Peer:       peer,
			MultiMedia: media,
		})
		if err != nil {
			return mapRPCError(err)
		}
		results, err = extractMediaGroupResults(updates, len(reqs))
		return err
	})
	return results, err
}

// buildAlbumInputMedia turns one uploaded file into an InputMedia entry for
// messages.sendMultiMedia. Captions live on the wrapping InputSingleMedia, so
// the media itself stays caption-free. The mapping mirrors the single-upload
// path in UploadMedia: photo kind → uploaded photo, video kind → document
// with a video attribute block, everything else → plain document. thumb must
// be an already-uploaded thumbnail input file (nil for none).
func buildAlbumInputMedia(uploaded tg.InputFileClass, thumb tg.InputFileClass, req tgtelegram.UploadRequest) tg.InputMediaClass {
	if req.Kind == tgtelegram.KindPhoto {
		return &tg.InputMediaUploadedPhoto{File: uploaded}
	}

	attributes := []tg.DocumentAttributeClass{
		// Raw InputMediaUploadedDocument carries no filename field; native
		// clients learn the name from this attribute alone.
		&tg.DocumentAttributeFilename{FileName: req.FileName},
	}
	if req.Kind == tgtelegram.KindVideo {
		attr := &tg.DocumentAttributeVideo{}
		if req.Video != nil {
			attr.Duration = req.Video.DurationSeconds
			attr.W = req.Video.Width
			attr.H = req.Video.Height
			attr.SupportsStreaming = req.Video.SupportsStreaming
		}
		attributes = append(attributes, attr)
	}
	doc := &tg.InputMediaUploadedDocument{
		File:       uploaded,
		Thumb:      thumb,
		MimeType:   req.MIME,
		Attributes: attributes,
	}
	return doc
}

// extractMediaGroupResults pairs the update stream back to the requests:
// exactly one channel message per member, ids ascending, all sharing the
// group's single grouped id.
func extractMediaGroupResults(updates tg.UpdatesClass, want int) ([]tgtelegram.UploadResult, error) {
	var list []tg.UpdateClass
	switch u := updates.(type) {
	case *tg.Updates:
		list = u.Updates
	case *tg.UpdatesCombined:
		list = u.Updates
	default:
		return nil, errors.New("message id not found")
	}
	var msgs []*tg.Message
	for _, upd := range list {
		switch m := upd.(type) {
		case *tg.UpdateNewChannelMessage:
			if msg, ok := m.Message.(*tg.Message); ok {
				msgs = append(msgs, msg)
			}
		case *tg.UpdateNewMessage:
			if msg, ok := m.Message.(*tg.Message); ok {
				msgs = append(msgs, msg)
			}
		}
	}
	if len(msgs) != want {
		return nil, errors.Errorf("media group returned %d messages, want %d", len(msgs), want)
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })
	out := make([]tgtelegram.UploadResult, 0, len(msgs))
	gid := int64(0)
	for i, msg := range msgs {
		id, _ := msg.GetGroupedID()
		if i == 0 {
			if id == 0 {
				return nil, errors.New("media group has no grouped id")
			}
			gid = id
		}
		if id != gid {
			return nil, errors.New("media group members disagree on grouped id")
		}
		out = append(out, tgtelegram.UploadResult{MessageID: msg.ID, GroupedID: gid})
	}
	return out, nil
}
