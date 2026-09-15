package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// Index-op collectors: fold the three metadata sources of a scan pass
// (album inventories, per-file manifest records, captions) into index
// operations. Kept with the scan state in scan.go.

func (r *scanRun) tombstoneRow(ctx context.Context, mediaID int, canonicalPath string) {
	if !r.opts.IncludeDeleted || canonicalPath == "" {
		return
	}
	r.tombstones++
	_, _ = r.app.DB.Raw().ExecContext(ctx, `update files set status='deleted', node_id=null, updated_at=? where channel_id=? and message_id=? and status in ('active','missing')`,
		r.now, r.channelID, mediaID)
}

// collectAlbumOps indexes members of parsed td-album:v1 inventories.
func (r *scanRun) collectAlbumOps(ctx context.Context, inRoot func(string) bool, ops *[]scanIndexOp) {
	for _, inv := range r.albumInventories {
		for _, f := range inv.meta.Files {
			if r.seenMedia[f.MessageID] {
				continue
			}
			media, ok := r.byID[f.MessageID]
			if !ok {
				got, gerr := r.app.TG.GetMessage(ctx, r.tgChID, f.MessageID)
				if gerr != nil {
					r.recordScanError(ctx, f.MessageID, apperr.ErrManifestInvalid, "album member not found", f.CanonicalPath)
					continue
				}
				media = got
			}
			meta := manifest.ParsedMeta{
				CanonicalPath: f.CanonicalPath,
				DisplayName:   f.DisplayName,
				Size:          f.Size,
				Hash:          f.Hash,
				MIME:          f.MIME,
			}
			fillMetaFromMedia(&meta, media)
			if meta.CanonicalPath == "" || !inRoot(meta.CanonicalPath) {
				continue
			}
			r.seen[meta.CanonicalPath] = true
			r.seenMedia[f.MessageID] = true
			*ops = append(*ops, scanIndexOp{messageID: f.MessageID, manifestMsgID: inv.replyID, manifestChat: inv.chat, meta: meta})
		}
	}
}

// collectManifestReplyOps resolves td-manifest:v1 replies for media messages.
// Reconciliation precedence: the manifest reply only wins when the media
// caption carries no machine metadata of its own — a tombstoned or self-described
// caption is authoritative regardless of what the reply claims.
func (r *scanRun) collectManifestReplyOps(ctx context.Context, inRoot func(string) bool, ops *[]scanIndexOp) {
	for mediaID, resolved := range r.manifestByMedia {
		if r.seenMedia[mediaID] {
			continue
		}
		if _, comment := r.commentByMedia[mediaID]; comment {
			// A comment record outranks the in-channel reply (ADR 0018
			// precedence); the comment pass owns this message.
			continue
		}
		media, ok := r.byID[mediaID]
		if !ok {
			got, gerr := r.app.TG.GetMessage(ctx, r.tgChID, mediaID)
			if gerr != nil {
				r.recordScanError(ctx, mediaID, apperr.ErrManifestInvalid, "manifest target not found", resolved.meta.CanonicalPath)
				continue
			}
			media = got
		}
		if manifest.HasMachineMeta(media.Caption) {
			// The caption is authoritative (pass 3 owns this message).
			continue
		}
		if resolved.meta.Deleted {
			r.seenMedia[mediaID] = true
			r.tombstoneRow(ctx, mediaID, resolved.meta.CanonicalPath)
			continue
		}
		meta := resolved.meta
		fillMetaFromMedia(&meta, media)
		if meta.CanonicalPath == "" || !inRoot(meta.CanonicalPath) {
			continue
		}
		r.seen[meta.CanonicalPath] = true
		r.seenMedia[mediaID] = true
		*ops = append(*ops, scanIndexOp{messageID: mediaID, manifestMsgID: resolved.msgID, meta: meta})
	}
}

// collectCaptionOps parses td:v1 captions carried by the media/text messages
// themselves, including tombstones.
func (r *scanRun) collectCaptionOps(ctx context.Context, inRoot func(string) bool, ops *[]scanIndexOp) {
	ids := make([]int, 0, len(r.byID))
	for id := range r.byID {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		msg := r.byID[id]
		if r.seenMedia[msg.ID] || manifest.IsAlbumReply(msg.Text) || isPerFileManifestReply(msg) {
			continue
		}
		// A caption tombstone is sticky: it outranks even a newer live
		// comment record (the comment edit may have failed during delete).
		if manifest.HasMachineMeta(msg.Caption) {
			if cm, err := manifest.ParseCaption(msg.Caption); err == nil && cm.Deleted {
				r.seenMedia[msg.ID] = true
				r.tombstoneRow(ctx, msg.ID, cm.CanonicalPath)
				continue
			}
		}
		if rec, comment := r.commentByMedia[msg.ID]; comment {
			// The comment thread carries the machine record (ADR 0018);
			// captions of comment-carrier rows are human-only.
			if rec.meta.Deleted {
				r.seenMedia[msg.ID] = true
				r.tombstoneRow(ctx, msg.ID, rec.meta.CanonicalPath)
				continue
			}
			meta := rec.meta
			fillMetaFromMedia(&meta, msg)
			if meta.CanonicalPath == "" || !inRoot(meta.CanonicalPath) {
				continue
			}
			r.seen[meta.CanonicalPath] = true
			r.seenMedia[msg.ID] = true
			*ops = append(*ops, scanIndexOp{messageID: msg.ID, manifestMsgID: rec.msgID, manifestChat: r.manifestChat, meta: meta})
			continue
		}
		if msg.Caption == "" && msg.Text == "" {
			continue
		}
		var meta manifest.ParsedMeta
		var parseErr error
		switch {
		case msg.Caption != "":
			if !manifest.HasMachineMeta(msg.Caption) {
				// Native / unmanaged captions are not managed messages.
				continue
			}
			meta, parseErr = manifest.ParseCaption(msg.Caption)
		case msg.Text != "" && manifest.HasMachineMeta(msg.Text) && !strings.HasPrefix(strings.TrimSpace(msg.Text), "td-manifest:"):
			meta, parseErr = manifest.ParseCaption(msg.Text)
		default:
			// Manifest replies are resolved via manifestByMedia; other text is unmanaged.
			continue
		}
		if parseErr != nil {
			r.recordScanError(ctx, msg.ID, apperr.ErrManifestInvalid, parseErr.Error(), truncate(msg.Caption+msg.Text, 200))
			continue
		}
		if meta.Deleted {
			r.seenMedia[msg.ID] = true
			r.tombstoneRow(ctx, msg.ID, meta.CanonicalPath)
			continue
		}
		var manifestMsgID int
		if meta.ManifestReply {
			resolved, ok := r.manifestByMedia[msg.ID]
			if !ok {
				r.recordScanError(ctx, msg.ID, apperr.ErrManifestInvalid, "manifest reply not found", truncate(msg.Caption, 200))
				continue
			}
			if resolved.meta.Deleted {
				r.seenMedia[msg.ID] = true
				r.tombstoneRow(ctx, msg.ID, resolved.meta.CanonicalPath)
				continue
			}
			meta = resolved.meta
			manifestMsgID = resolved.msgID
		}
		if meta.CanonicalPath == "" || !inRoot(meta.CanonicalPath) {
			continue
		}
		fillMetaFromMedia(&meta, msg)
		r.seen[meta.CanonicalPath] = true
		r.seenMedia[msg.ID] = true
		*ops = append(*ops, scanIndexOp{messageID: msg.ID, manifestMsgID: manifestMsgID, meta: meta})
	}
}

// fillMetaFromMedia backfills metadata the manifest omitted from the message.
func fillMetaFromMedia(meta *manifest.ParsedMeta, media telegram.Message) {
	if meta.Size == 0 && media.FileSize > 0 {
		meta.Size = media.FileSize
	}
	if meta.MIME == "" && media.MIME != "" {
		meta.MIME = media.MIME
	}
	if meta.DisplayName == "" && media.FileName != "" {
		meta.DisplayName = media.FileName
	}
}

// recordMissingAlbumInventories flags grouped media whose td-album:v1
// inventory never arrived, so a whole album cannot silently vanish from the
// index. Members are recorded as errors, not indexed.
func (r *scanRun) recordMissingAlbumInventories(ctx context.Context) {
	gids := make([]int64, 0, len(r.albumGroups))
	for gid := range r.albumGroups {
		if r.coveredGroups[gid] || r.albumCorrupt[gid] {
			continue
		}
		gids = append(gids, gid)
	}
	sort.Slice(gids, func(i, j int) bool { return gids[i] < gids[j] })
	for _, gid := range gids {
		members := r.albumGroups[gid]
		indexed := false
		for _, id := range members {
			if r.seenMedia[id] {
				indexed = true
				break
			}
		}
		if indexed {
			// Some member carried its own metadata (legacy layout); the group
			// is reconstructable without an inventory.
			continue
		}
		minID := members[0]
		for _, id := range members[1:] {
			if id < minID {
				minID = id
			}
		}
		r.recordScanError(ctx, minID, apperr.ErrAlbumInventoryInvalid,
			fmt.Sprintf("album inventory missing for grouped media (grouped_id=%d, %d members)", gid, len(members)),
			fmt.Sprintf("grouped_id=%d", gid))
	}
}
