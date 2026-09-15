package service

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/pathcodec"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"

	"lukechampine.com/blake3"
)

// hashBackfillItem is one per-file result of RepairHash.
type hashBackfillItem struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

// hashTarget is one active file row adopted without a content hash.
type hashTarget struct {
	id                  int64
	path                string
	msgID, manID        int
	manChat, name, mime string
	size                int64
}

// countingHasher hashes the downloaded bytes while counting them, so the
// digest and the true byte size come out of one pass.
type countingHasher struct {
	h *blake3.Hasher
	n int64
}

func (w *countingHasher) Write(p []byte) (int, error) {
	// blake3 writes never fail; the digest accumulates in memory only.
	_, _ = w.h.Write(p)
	w.n += int64(len(p))
	return len(p), nil
}

// RepairHash backfills the BLAKE3 content hash of every active file under
// remotePath ("" = the whole channel) that was adopted without one. Each
// file is downloaded once: the digest lands in the index AND in the machine
// record on Telegram — a SQLite-only backfill would be wiped by the next
// scan, because the channel record would still claim h=-. Album members get
// their entry updated inside the group's one td-album:v1 inventory. A
// download also repairs a zero/missing size, which native photo imports
// never recorded.
func (a *App) RepairHash(ctx context.Context, remotePath string) (map[string]any, error) {
	channelID, _, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	tgChID, err := a.tgChannelID(ctx)
	if err != nil {
		return nil, err
	}
	root := "/"
	if remotePath != "" {
		root, err = fsmodel.NormalizeCanonicalPath(remotePath)
		if err != nil {
			return nil, err
		}
	}
	inRoot := func(p string) bool {
		return root == "/" || p == root || strings.HasPrefix(p, root+"/")
	}

	rows, err := a.DB.Raw().QueryContext(ctx, `
		select id, canonical_path, message_id, coalesce(manifest_message_id,0), coalesce(manifest_chat_tg_id,''), display_name, coalesce(size,0), coalesce(mime,'')
		from files
		where channel_id=? and status='active' and message_id is not null
		  and (content_hash is null or content_hash='')`, channelID)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "list files missing hash", err)
	}
	var targets []hashTarget
	for rows.Next() {
		var r hashTarget
		if err := rows.Scan(&r.id, &r.path, &r.msgID, &r.manID, &r.manChat, &r.name, &r.size, &r.mime); err != nil {
			_ = rows.Close()
			return nil, apperr.Wrap(apperr.ErrDB, "scan files missing hash", err)
		}
		if inRoot(r.path) {
			targets = append(targets, r)
		}
	}
	_ = rows.Close()

	existingSlugs := a.loadSlugMap(ctx, channelID)
	now := time.Now().UTC().Format(time.RFC3339)
	items := make([]hashBackfillItem, 0, len(targets))
	backfilled, failed := 0, 0
	for _, r := range targets {
		item := hashBackfillItem{Path: r.path}
		lockErr := a.withLocks(ctx, lockKeysForPaths(channelID, r.path), func(ctx context.Context) error {
			return a.repairHashTarget(ctx, r, channelID, tgChID, existingSlugs, now)
		})
		switch {
		case lockErr != nil:
			var nf *telegram.MessageNotFoundError
			if errors.As(lockErr, &nf) {
				item.Action = "failed"
				item.Reason = "media message not found on Telegram"
			} else {
				item.Action = "failed"
				item.Reason = lockErr.Error()
			}
			failed++
		default:
			item.Action = "backfilled"
			backfilled++
		}
		items = append(items, item)
	}
	return map[string]any{
		"backfilled": backfilled,
		"failed":     failed,
		"total":      len(targets),
		"items":      items,
	}, nil
}

// repairHashTarget backfills one file's hash: download and hash the media,
// update the machine record on its carrier, then the index. Telegram is the
// source of truth, so the record edit must land first or the next scan drops
// the hash again.
func (a *App) repairHashTarget(ctx context.Context, r hashTarget, channelID, tgChID int64, existingSlugs map[string]string, now string) error {
	hw := &countingHasher{h: blake3.New(32, nil)}
	if err := a.TG.DownloadMedia(ctx, tgChID, r.msgID, hw); err != nil {
		return err
	}
	hash := "blake3:" + hex.EncodeToString(hw.h.Sum(nil))
	meta := manifest.FileMeta{
		CanonicalPath: r.path,
		DisplayName:   r.name,
		ParentHuman:   fsmodel.HumanParent(r.path),
		Size:          r.size,
		Hash:          hash,
		MIME:          r.mime,
	}
	tags, _, err := pathcodec.GenerateChain(r.path, existingSlugs)
	if err != nil {
		return err
	}
	meta.Tags = tags

	carrier := a.manifestCarrier(r.manChat)
	if album, ok, aerr := a.loadAlbumManifest(ctx, tgChID, carrier, r.manID); aerr == nil && ok {
		updated := albumReplacePath(album, r.msgID, r.path)
		for i, f := range updated.Files {
			if f.MessageID == r.msgID {
				updated.Files[i].Hash = hash
				if updated.Files[i].Size == 0 {
					updated.Files[i].Size = hw.n
				}
			}
		}
		if _, uerr := a.writeAlbumManifest(ctx, channelID, tgChID, carrier, r.manID, albumFirstMediaID(updated), updated); uerr != nil {
			return uerr
		}
	} else if r.manID > 0 {
		// Per-file manifest record (comment or legacy in-channel reply,
		// routed by the carrier): rewrite it with the hash.
		body := manifest.RenderManifestReplyFitting(meta, manifest.DefaultTextBudget, a.Cfg.Caption.MarginUTF16Units)
		if err := carrier.Edit(ctx, tgChID, r.manID, body); err != nil {
			return telegram.MapError(err)
		}
	} else {
		// Caption carrier: the compact line lives on the media caption
		// itself.
		caption, _, _, cerr := manifest.RenderLegacyCaption(meta, a.Cfg.Caption.SafeMediaCaptionUTF16Units, a.Cfg.Caption.MarginUTF16Units)
		if cerr != nil {
			return cerr
		}
		if err := a.TG.EditCaption(ctx, tgChID, r.msgID, caption); err != nil && !isMessageGone(err) {
			return telegram.MapError(err)
		}
	}

	newSize := r.size
	if newSize == 0 {
		newSize = hw.n
	}
	if _, uerr := a.DB.Raw().ExecContext(ctx,
		`update files set content_hash=?, size=case when size is null or size=0 then ? else size end, updated_at=? where id=?`,
		hash, newSize, now, r.id); uerr != nil {
		return apperr.Wrap(apperr.ErrDB, "store content hash", uerr)
	}
	return nil
}
