package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/publisher"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// ensureTelegramManifests writes the missing reconstructable machine
// records: one td-album:v1 comment per media group, and one td-manifest:v1
// comment per ungrouped adopted file that has no machine record. Legacy
// in-channel replies found on the channel are converted to comment threads.
func (a *App) ensureTelegramManifests(ctx context.Context, channelID, tgChID int64, manifestChat string, history []telegram.Message, opts AdoptOptions, out *AdoptResult) error {
	byID := map[int]telegram.Message{}
	var albumReplies []telegram.Message
	perFileReply := map[int]int{} // media id -> reply id
	for _, msg := range history {
		byID[msg.ID] = msg
		if manifest.IsAlbumReply(msg.Text) {
			albumReplies = append(albumReplies, msg)
			continue
		}
		if isPerFileManifestReply(msg) && msg.ReplyTo != nil {
			perFileReply[*msg.ReplyTo] = msg.ID
		}
	}

	rows, err := a.DB.Raw().QueryContext(ctx, `
		select id, message_id, coalesce(manifest_message_id,0), coalesce(manifest_chat_tg_id,''), canonical_path, display_name, coalesce(size,0), coalesce(content_hash,''), coalesce(mime,'')
		from files where channel_id=? and status='active' and message_id is not null`, channelID)
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "list files for album manifest", err)
	}
	type rec struct {
		fileID, msgID, manID   int
		manChat                string
		path, name, hash, mime string
		size                   int64
	}
	byMsg := map[int]rec{}
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.fileID, &r.msgID, &r.manID, &r.manChat, &r.path, &r.name, &r.size, &r.hash, &r.mime); err != nil {
			_ = rows.Close()
			return err
		}
		byMsg[r.msgID] = r
	}
	_ = rows.Close()

	var media []telegram.Message
	for _, msg := range history {
		if manifest.IsAlbumReply(msg.Text) || isPerFileManifestReply(msg) {
			continue
		}
		if _, ok := byMsg[msg.ID]; ok {
			media = append(media, msg)
		}
	}
	albums, singles := groupAlbums(media)

	albumByGroup := map[int64]telegram.Message{}
	for _, reply := range albumReplies {
		parsed, err := manifest.ParseAlbumReply(reply.Text)
		if err == nil && parsed.GroupedID != 0 {
			albumByGroup[parsed.GroupedID] = reply
		}
	}

	gids := make([]int64, 0, len(albums))
	for gid := range albums {
		gids = append(gids, gid)
	}
	sort.Slice(gids, func(i, j int) bool { return gids[i] < gids[j] })

	now := time.Now().UTC().Format(time.RFC3339)
	for _, gid := range gids {
		members := albums[gid]
		sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
		meta := manifest.AlbumMeta{GroupedID: gid}
		memberPaths := make([]string, 0, len(members))
		converted := false
		for _, m := range members {
			r := byMsg[m.ID]
			if r.manChat != "" {
				converted = true
			}
			meta.Files = append(meta.Files, manifest.AlbumFile{
				MessageID:     m.ID,
				CanonicalPath: r.path,
				DisplayName:   r.name,
				Size:          r.size,
				Hash:          r.hash,
				MIME:          r.mime,
			})
			memberPaths = append(memberPaths, r.path)
		}
		body, err := manifest.RenderAlbumReplyFitting(meta, manifest.DefaultTextBudget, a.Cfg.Caption.MarginUTF16Units)
		if err != nil {
			return err
		}
		existing, has := albumByGroup[gid]
		item := AdoptPlanItem{
			MessageID: members[0].ID,
			Kind:      "album",
			GroupedID: gid,
			Action:    "album-manifest",
			Reason:    fmt.Sprintf("one inventory comment for %d files", len(members)),
		}
		if converted {
			item.Action = "skip"
			item.Reason = "album inventory already a comment thread"
			out.Skipped++
			out.Items = append(out.Items, item)
			continue
		}
		if opts.DryRun {
			out.Adopted++
			out.Items = append(out.Items, item)
			continue
		}
		// The inventory rewrite locks every member path (sorted by the lock
		// helper) so it cannot race a concurrent move or delete of a member.
		comment := a.manifestCarrier(manifestChat)
		legacy := a.manifestCarrier("")
		var sendErr error
		legacyOnly := false
		lockErr := a.withLocks(ctx, lockKeysForPaths(channelID, memberPaths...), func(ctx context.Context) error {
			id, err := comment.Send(ctx, tgChID, members[0].ID, body)
			if err != nil {
				var nf *telegram.MessageNotFoundError
				if !errors.As(err, &nf) {
					sendErr = err
					return nil
				}
				// Posts published before the discussion group was linked
				// have no comment thread (Telegram creates threads only for
				// posts sent after linking). Their record stays on the
				// legacy in-channel reply carrier.
				legacyOnly = true
				if has {
					if existing.Text == body {
						return nil
					}
					if err := legacy.Edit(ctx, tgChID, existing.ID, body); err != nil {
						sendErr = err
						return nil
					}
					_, _ = a.DB.Raw().ExecContext(ctx, `update files set manifest_message_id=?, manifest_chat_tg_id='', updated_at=? where channel_id=? and message_id in (`+intJoin(msgIDs(members))+`)`, existing.ID, now, channelID)
					return nil
				}
				rid, rerr := legacy.Send(ctx, tgChID, members[0].ID, body)
				if rerr != nil {
					sendErr = rerr
					return nil
				}
				_, _ = a.DB.Raw().ExecContext(ctx, `update files set manifest_message_id=?, manifest_chat_tg_id='', updated_at=? where channel_id=? and message_id in (`+intJoin(msgIDs(members))+`)`, rid, now, channelID)
				return nil
			}
			if has {
				// Convert: the legacy in-channel reply is superseded by the
				// comment thread and deleted.
				if err := legacy.Delete(ctx, tgChID, existing.ID); err != nil && !isMessageGone(err) {
					sendErr = err
					return nil
				}
			}
			_, _ = a.DB.Raw().ExecContext(ctx, `update files set manifest_message_id=?, manifest_chat_tg_id=?, updated_at=? where channel_id=? and message_id in (`+intJoin(msgIDs(members))+`)`, id, comment.ChatID(), now, channelID)
			return nil
		})
		if lockErr != nil {
			return lockErr
		}
		if sendErr != nil {
			item.Action = "fail"
			item.Reason = sendErr.Error()
			out.Failed++
			out.Items = append(out.Items, item)
			if !opts.ContinueErr {
				return telegram.MapError(sendErr)
			}
			continue
		}
		if legacyOnly {
			item.Action = "skip"
			item.Reason = "post predates the linked discussion group (no comment thread); legacy reply kept"
			out.Skipped++
			out.Items = append(out.Items, item)
			continue
		}
		out.Adopted++
		out.Items = append(out.Items, item)
	}

	sort.Slice(singles, func(i, j int) bool { return singles[i].ID < singles[j].ID })
	for _, msg := range singles {
		r := byMsg[msg.ID]
		if manifest.HasMachineMeta(messageBody(msg)) || perFileReply[msg.ID] > 0 {
			continue
		}
		if r.manChat != "" {
			continue
		}
		item := AdoptPlanItem{
			MessageID: msg.ID, Kind: adoptKind(msg), Path: r.path,
			Action: "manifest", Reason: "one reconstructable comment for ungrouped file",
		}
		if opts.DryRun {
			out.Adopted++
			out.Items = append(out.Items, item)
			continue
		}
		reply := manifest.RenderManifestReplyFitting(manifest.FileMeta{
			CanonicalPath: r.path,
			DisplayName:   r.name,
			ParentHuman:   fsmodel.HumanParent(r.path),
			Size:          r.size,
			Hash:          r.hash,
			MIME:          r.mime,
		}, manifest.DefaultTextBudget, a.Cfg.Caption.MarginUTF16Units)
		comment := a.manifestCarrier(manifestChat)
		legacy := a.manifestCarrier("")
		var sendErr error
		legacyOnly := false
		lockErr := a.withLocks(ctx, lockKeysForPaths(channelID, r.path), func(ctx context.Context) error {
			id, err := comment.Send(ctx, tgChID, msg.ID, reply)
			if err != nil {
				var nf *telegram.MessageNotFoundError
				if !errors.As(err, &nf) {
					sendErr = err
					return nil
				}
				// Pre-link post: no comment thread; keep the legacy reply.
				legacyOnly = true
				rid, rerr := legacy.Send(ctx, tgChID, msg.ID, reply)
				if rerr != nil {
					sendErr = rerr
					return nil
				}
				_, _ = a.DB.Raw().ExecContext(ctx, `update files set manifest_message_id=?, manifest_chat_tg_id='', updated_at=? where id=?`, rid, now, r.fileID)
				return nil
			}
			_, _ = a.DB.Raw().ExecContext(ctx, `update files set manifest_message_id=?, manifest_chat_tg_id=?, updated_at=? where id=?`, id, comment.ChatID(), now, r.fileID)
			return nil
		})
		if lockErr != nil {
			return lockErr
		}
		if sendErr != nil {
			item.Action = "fail"
			item.Reason = sendErr.Error()
			out.Failed++
			out.Items = append(out.Items, item)
			if !opts.ContinueErr {
				return telegram.MapError(sendErr)
			}
			continue
		}
		if legacyOnly {
			item.Action = "skip"
			item.Reason = "post predates the linked discussion group (no comment thread); legacy reply kept"
			out.Skipped++
			out.Items = append(out.Items, item)
			continue
		}
		out.Adopted++
		out.Items = append(out.Items, item)
	}
	return nil
}

func msgIDs(msgs []telegram.Message) []int {
	out := make([]int, len(msgs))
	for i, m := range msgs {
		out[i] = m.ID
	}
	return out
}

func intJoin(ids []int) string {
	if len(ids) == 0 {
		return "0"
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("%d", id)
	}
	return strings.Join(parts, ",")
}

func isPerFileManifestReply(msg telegram.Message) bool {
	if msg.Kind == telegram.KindDocument || msg.Kind == telegram.KindPhoto {
		return false
	}
	if msg.FileName != "" {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(msg.Text), "td-manifest:v1")
}

// loadAlbumManifest reads the album inventory through the given carrier: a
// comment for the comment carrier, the drive channel for the legacy carrier
// (ADR 0018).
func (a *App) loadAlbumManifest(ctx context.Context, tgChID int64, carrier telegram.ManifestCarrier, manifestID int) (manifest.AlbumMeta, bool, error) {
	if manifestID <= 0 {
		return manifest.AlbumMeta{}, false, nil
	}
	msg, err := carrier.Get(ctx, tgChID, manifestID)
	if err != nil {
		return manifest.AlbumMeta{}, false, err
	}
	if !manifest.IsAlbumReply(msg.Text) {
		return manifest.AlbumMeta{}, false, nil
	}
	meta, err := manifest.ParseAlbumReply(msg.Text)
	if err != nil {
		return manifest.AlbumMeta{}, false, err
	}
	return meta, true, nil
}

// writeAlbumManifest edits or posts the one td-album:v1 inventory of a
// group through the given carrier and records it on every member row.
func (a *App) writeAlbumManifest(ctx context.Context, channelID, tgChID int64, carrier telegram.ManifestCarrier, manifestID int, firstMediaID int, meta manifest.AlbumMeta) (int, error) {
	body, err := manifest.RenderAlbumReplyFitting(meta, manifest.DefaultTextBudget, a.Cfg.Caption.MarginUTF16Units)
	if err != nil {
		return 0, err
	}
	id := manifestID
	if id > 0 {
		if err := carrier.Edit(ctx, tgChID, id, body); err != nil {
			return 0, telegram.MapError(err)
		}
	} else {
		id, err = carrier.Send(ctx, tgChID, firstMediaID, body)
		if err != nil {
			return 0, telegram.MapError(err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	ids := make([]int, len(meta.Files))
	for i, f := range meta.Files {
		ids[i] = f.MessageID
	}
	_, _ = a.DB.Raw().ExecContext(ctx, `update files set manifest_message_id=?, manifest_chat_tg_id=?, updated_at=? where channel_id=? and message_id in (`+intJoin(ids)+`)`, id, carrier.ChatID(), now, channelID)
	return id, nil
}

func (a *App) reindexAlbumMember(ctx context.Context, channelID int64, fileID int64, messageID, manifestID int, dest, hash, mime string, size int64) error {
	existingSlugs, err := a.loadExistingSlugs(ctx, channelID)
	if err != nil {
		return err
	}
	man := manifestID
	if _, err := a.publisher().Reindex(ctx, publisher.ReindexRequest{
		ChannelRowID:  channelID,
		FileID:        fileID,
		MessageID:     messageID,
		ManifestMsgID: &man,
		Meta: manifest.ParsedMeta{
			CanonicalPath: dest,
			DisplayName:   fsmodel.BaseName(dest),
			Size:          size,
			Hash:          hash,
			MIME:          mime,
		},
		ExistingSlugs: existingSlugs,
	}); err != nil {
		return err
	}
	return a.DB.RunDirectoryGC(ctx, channelID)
}

func albumWithout(meta manifest.AlbumMeta, messageID int) manifest.AlbumMeta {
	out := manifest.AlbumMeta{GroupedID: meta.GroupedID}
	for _, f := range meta.Files {
		if f.MessageID != messageID {
			out.Files = append(out.Files, f)
		}
	}
	return out
}

func albumReplacePath(meta manifest.AlbumMeta, messageID int, dest string) manifest.AlbumMeta {
	out := manifest.AlbumMeta{GroupedID: meta.GroupedID, Files: append([]manifest.AlbumFile(nil), meta.Files...)}
	for i, f := range out.Files {
		if f.MessageID == messageID {
			out.Files[i].CanonicalPath = dest
			out.Files[i].DisplayName = fsmodel.BaseName(dest)
		}
	}
	return out
}

func albumFirstMediaID(meta manifest.AlbumMeta) int {
	if len(meta.Files) == 0 {
		return 0
	}
	min := meta.Files[0].MessageID
	for _, f := range meta.Files[1:] {
		if f.MessageID < min {
			min = f.MessageID
		}
	}
	return min
}
