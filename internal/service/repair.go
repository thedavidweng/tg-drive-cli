package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/publisher"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// RepairPending resolves stale pending rows and expired operation locks.
// Only rows older than the lock TTL whose path is not currently locked are
// touched, so a live in-flight upload is never deleted or duplicated.
func (a *App) RepairPending(ctx context.Context) (map[string]any, error) {
	channelID, _, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	ttl := time.Duration(a.Cfg.Locks.TTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = 900 * time.Second // same default as withLocks
	}
	staleCutoff := time.Now().UTC().Add(-ttl).Format(time.RFC3339)
	locksCleared := int64(0)
	if res, err := a.DB.Raw().ExecContext(ctx, `delete from operation_locks where expires_at < ?`, now); err == nil {
		locksCleared, _ = res.RowsAffected()
	}
	rows, err := a.DB.Raw().QueryContext(ctx, `select id, canonical_path, original_local_path, message_id from files where channel_id=? and status='pending' and updated_at < ?`, channelID, staleCutoff)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "list pending", err)
	}
	type pendingRow struct {
		id    int64
		path  string
		local sql.NullString
		msgID sql.NullInt64
	}
	var pending []pendingRow
	for rows.Next() {
		var r pendingRow
		if err := rows.Scan(&r.id, &r.path, &r.local, &r.msgID); err != nil {
			_ = rows.Close()
			return nil, apperr.Wrap(apperr.ErrDB, "scan pending", err)
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, apperr.Wrap(apperr.ErrDB, "list pending", err)
	}
	_ = rows.Close()
	repaired, invalid, orphaned, skipped := 0, 0, 0, 0
	for _, r := range pending {
		// A path lock held right now means an operation is in flight (e.g. an
		// upload adopting this very row); leave it alone.
		if held, err := a.DB.LockHeld(ctx, sqlitestore.LockKey(channelID, r.path)); err == nil && held {
			skipped++
			continue
		}
		// Status flips hold the row's path lock so they cannot race a
		// concurrent upload adopting the same row.
		flip := func(status string) bool {
			ok := true
			err := a.withLocks(ctx, lockKeysForPaths(channelID, r.path), func(ctx context.Context) error {
				_ = a.DB.DeleteUploadStateByFile(ctx, r.id)
				_, _ = a.DB.Raw().ExecContext(ctx, `update files set status=?, updated_at=? where id=?`, status, now, r.id)
				return nil
			})
			if err != nil {
				ok = false
				skipped++
			}
			return ok
		}
		switch {
		case r.msgID.Valid:
			// Upload reached Telegram but was never promoted; hand off to
			// orphan repair which can complete or delete it. The upload
			// finished, so any resumable part state is garbage.
			if flip("orphaned") {
				orphaned++
			}
		case r.local.Valid && r.local.String != "":
			if _, statErr := a.files().Stat(ctx, r.local.String); statErr == nil {
				// Drop the stale row (and its state) and retry the upload
				// fresh; UploadFile takes the path lock itself.
				err := a.withLocks(ctx, lockKeysForPaths(channelID, r.path), func(ctx context.Context) error {
					_ = a.DB.DeleteUploadStateByFile(ctx, r.id)
					_, _ = a.DB.Raw().ExecContext(ctx, `delete from files where id=?`, r.id)
					return nil
				})
				if err != nil {
					skipped++
					continue
				}
				if _, err := a.UploadFile(ctx, r.local.String, r.path, ConflictSkip, false); err == nil {
					repaired++
					continue
				}
				invalid++
				continue
			}
			if flip("invalid") {
				invalid++
			}
		default:
			if flip("invalid") {
				invalid++
			}
		}
	}
	return map[string]any{"repaired": repaired, "invalid": invalid, "orphaned": orphaned, "skipped": skipped, "locks_cleared": locksCleared}, nil
}

// RepairOrphaned completes or removes uploads whose media message exists on
// Telegram but whose index promotion failed.
func (a *App) RepairOrphaned(ctx context.Context, deleteOrphans bool) (map[string]any, error) {
	channelID, _, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	tgChID, err := a.tgChannelID(ctx)
	if err != nil {
		return nil, err
	}
	manifestChat, err := a.discussionChatID(ctx, channelID)
	if err != nil {
		return nil, err
	}
	rows, err := a.DB.Raw().QueryContext(ctx, `
		select id, canonical_path, display_name, coalesce(size,0), coalesce(content_hash,''), coalesce(mime,''), message_id
		from files where channel_id=? and status='orphaned'`, channelID)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "list orphaned", err)
	}
	type orphanRow struct {
		id          int64
		path, name  string
		size        int64
		hash, mimeT string
		msgID       sql.NullInt64
	}
	var orphans []orphanRow
	for rows.Next() {
		var r orphanRow
		if err := rows.Scan(&r.id, &r.path, &r.name, &r.size, &r.hash, &r.mimeT, &r.msgID); err != nil {
			_ = rows.Close()
			return nil, apperr.Wrap(apperr.ErrDB, "scan orphaned", err)
		}
		orphans = append(orphans, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, apperr.Wrap(apperr.ErrDB, "list orphaned", err)
	}
	_ = rows.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	repaired, deleted, invalid := 0, 0, 0
	for _, r := range orphans {
		if !r.msgID.Valid {
			_ = a.DB.DeleteUploadStateByFile(ctx, r.id)
			_, _ = a.DB.Raw().ExecContext(ctx, `update files set status='invalid', updated_at=? where id=?`, now, r.id)
			invalid++
			continue
		}
		// Each orphan repair holds its path lock, so it cannot race a
		// concurrent move or delete of the same file.
		err := a.withLocks(ctx, lockKeysForPaths(channelID, r.path), func(ctx context.Context) error {
			if deleteOrphans {
				err := a.TG.DeleteMessage(ctx, tgChID, int(r.msgID.Int64))
				if err == nil || isMessageGone(err) {
					_ = a.DB.DeleteUploadStateByFile(ctx, r.id)
					_, _ = a.DB.Raw().ExecContext(ctx, `update files set status='deleted', node_id=null, updated_at=? where id=?`, now, r.id)
					deleted++
					return nil
				}
				invalid++
				return nil
			}
			// Complete the interrupted upload: regenerate metadata and resend the
			// manifest reply, then promote the row.
			meta := manifest.FileMeta{
				CanonicalPath: r.path,
				DisplayName:   r.name,
				ParentHuman:   fsmodel.HumanParent(r.path),
				Size:          r.size,
				Hash:          r.hash,
				MIME:          r.mimeT,
				Created:       now,
			}
			if _, err := a.publisher().Publish(ctx, publisher.PublishRequest{
				ChannelRowID:   channelID,
				ChannelID:      tgChID,
				FileID:         r.id,
				MessageID:      int(r.msgID.Int64),
				ManifestChatID: manifestChat,
				Meta:           meta,
				ExistingSlugs:  a.loadSlugMap(ctx, channelID),
				SetUploadedAt:  true,
			}); err != nil {
				return nil // stays orphaned for a later attempt
			}
			_ = a.DB.DeleteUploadStateByFile(ctx, r.id)
			repaired++
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{"repaired": repaired, "deleted": deleted, "invalid": invalid}, nil
}

// RepairPath re-renders and re-applies caption, manifest, and tags for one
// active file, repairing caption/manifest drift.
func (a *App) RepairPath(ctx context.Context, remotePath string) (map[string]any, error) {
	p, err := fsmodel.NormalizeCanonicalPath(remotePath)
	if err != nil {
		return nil, err
	}
	channelID, _, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	tgChID, err := a.tgChannelID(ctx)
	if err != nil {
		return nil, err
	}
	var fileID int64
	var messageID, manifestID sql.NullInt64
	var manifestChat string
	var displayName, contentHash, mimeType string
	var size int64
	err = a.DB.Raw().QueryRowContext(ctx, `select id, message_id, manifest_message_id, manifest_chat_tg_id, display_name, coalesce(size,0), coalesce(content_hash,''), coalesce(mime,'') from files where channel_id=? and canonical_path=? and status='active'`,
		channelID, p).Scan(&fileID, &messageID, &manifestID, &manifestChat, &displayName, &size, &contentHash, &mimeType)
	if err == sql.ErrNoRows {
		return nil, apperr.New(apperr.ErrRemoteNotFound, fmt.Sprintf("remote path %q not found", p))
	}
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "lookup file", err)
	}
	if !messageID.Valid {
		return nil, apperr.New(apperr.ErrRemoteNotFound, fmt.Sprintf("remote path %q not found", p))
	}
	existingSlugs := a.loadSlugMap(ctx, channelID)
	lockErr := a.withLocks(ctx, lockKeysForPaths(channelID, p), func(ctx context.Context) error {
		meta := manifest.FileMeta{
			CanonicalPath: p,
			DisplayName:   displayName,
			ParentHuman:   fsmodel.HumanParent(p),
			Size:          size,
			Hash:          contentHash,
			MIME:          mimeType,
		}
		oldMeta := meta
		oldMeta.Tags = nil
		manifestMsgID := 0
		if manifestID.Valid {
			manifestMsgID = int(manifestID.Int64)
		}
		carrier := a.manifestCarrier(manifestChat)
		if album, ok, err := a.loadAlbumManifest(ctx, tgChID, carrier, manifestMsgID); err != nil {
			return telegram.MapError(err)
		} else if ok {
			if _, err := a.writeAlbumManifest(ctx, channelID, tgChID, carrier, manifestMsgID, albumFirstMediaID(album), album); err != nil {
				return err
			}
			return nil
		}
		if _, err := a.publisher().Publish(ctx, publisher.PublishRequest{
			ChannelRowID:      channelID,
			ChannelID:         tgChID,
			FileID:            fileID,
			MessageID:         int(messageID.Int64),
			ManifestMsgID:     manifestMsgID,
			ManifestChatID:    manifestChat,
			Meta:              meta,
			ExistingSlugs:     existingSlugs,
			EditCaption:       true,
			IgnoreNotEditable: true,
			OldMeta:           &oldMeta,
		}); err != nil {
			return err
		}
		return nil
	})
	if lockErr != nil {
		return nil, lockErr
	}
	return map[string]any{"repaired": p}, nil
}

// RepairScanErrors reprocesses the channel and clears resolved scan errors.
func (a *App) RepairScanErrors(ctx context.Context) (map[string]any, error) {
	channelID, _, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	var before int
	_ = a.DB.Raw().QueryRowContext(ctx, `select count(*) from scan_errors where channel_id=? and status='pending'`, channelID).Scan(&before)
	if _, err := a.Scan(ctx, ScanOptions{Full: true, Repair: true}); err != nil {
		return nil, err
	}
	var after int
	_ = a.DB.Raw().QueryRowContext(ctx, `select count(*) from scan_errors where channel_id=? and status='pending'`, channelID).Scan(&after)
	return map[string]any{"resolved": before - after, "pending": after}, nil
}
