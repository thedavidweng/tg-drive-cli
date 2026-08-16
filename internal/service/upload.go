package service

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	"github.com/thedavidweng/tg-drive-cli/core/drive"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/model"
	"github.com/thedavidweng/tg-drive-cli/core/pathcodec"
	"github.com/thedavidweng/tg-drive-cli/core/ports"
	"github.com/thedavidweng/tg-drive-cli/core/publisher"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
	"lukechampine.com/blake3"
)

// App is the main application service.
type App struct {
	Cfg     config.Config
	DB      *sqlitestore.DB
	TG      telegram.Client
	Runtime *drive.Runtime
	// Channel optionally selects a configured channel by title or Telegram ID
	// (from --channel / TD_CHANNEL). Empty selects the first configured one.
	Channel string
	Render  func() bool // returns json mode
	// Progress optionally receives upload part confirmations.
	Progress telegram.UploadProgress
	// Index optionally overrides the file index used by the publisher.
	// Tests inject a failing index to cover the post-upload crash window.
	Index ports.FileIndex

	limitMu     sync.Mutex
	cachedLimit int64
}

func (a *App) files() ports.FileSystem {
	if a.Runtime == nil || a.Runtime.Files == nil {
		panic("runtime filesystem not configured")
	}
	return a.Runtime.Files
}

func (a *App) fileIndex() ports.FileIndex {
	if a.Index != nil {
		return a.Index
	}
	return a.DB
}

func (a *App) publisher() *publisher.Publisher {
	return publisher.New(a.TG, a.fileIndex(), publisher.Config{
		SafeMediaCaptionUTF16Units: a.Cfg.Caption.SafeMediaCaptionUTF16Units,
		MarginUTF16Units:           a.Cfg.Caption.MarginUTF16Units,
	})
}

func isMessageGone(err error) bool {
	var nf *telegram.MessageNotFoundError
	return errors.As(err, &nf)
}

func (a *App) recordPendingMessage(ctx context.Context, fileID int64, messageID int, now string) error {
	_, err := a.DB.Raw().ExecContext(ctx, `update files set message_id=?, updated_at=? where id=?`, messageID, now, fileID)
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "record uploaded message", err)
	}
	return nil
}

// abandonUploadedMedia runs after media exists on Telegram but publish/index
// failed. It deletes the media when possible; otherwise the pending row is
// marked orphaned with message_id so RepairPending will not re-upload.
func (a *App) abandonUploadedMedia(ctx context.Context, tgChID, fileID int64, messageID int, now string) (orphaned bool) {
	delErr := a.TG.DeleteMessage(ctx, tgChID, messageID)
	if delErr == nil || isMessageGone(delErr) {
		_, _ = a.DB.Raw().ExecContext(ctx, `delete from files where id=?`, fileID)
		return false
	}
	_, _ = a.DB.Raw().ExecContext(ctx, `update files set status='orphaned', message_id=?, updated_at=? where id=?`, messageID, now, fileID)
	return true
}

// ConflictPolicy for uploads/downloads.
type ConflictPolicy string

const (
	ConflictFail    ConflictPolicy = "fail"
	ConflictReplace ConflictPolicy = "replace"
	ConflictSkip    ConflictPolicy = "skip"
	ConflictRename  ConflictPolicy = "rename"
)

func (a *App) channelID(ctx context.Context) (int64, string, error) {
	var id int64
	var tgID, title string
	var err error
	if a.Channel != "" {
		err = a.DB.Raw().QueryRowContext(ctx, `select id, tg_channel_id, title from channels where title=? or tg_channel_id=? limit 1`,
			a.Channel, a.Channel).Scan(&id, &tgID, &title)
		if err == sql.ErrNoRows {
			return 0, "", apperr.New(apperr.ErrChannelNotFound, "channel not found: "+a.Channel)
		}
	} else {
		err = a.DB.Raw().QueryRowContext(ctx, `select id, tg_channel_id, title from channels limit 1`).Scan(&id, &tgID, &title)
		if err == sql.ErrNoRows {
			return 0, "", apperr.New(apperr.ErrChannelNotFound, "no channel configured; run: td init <local-root> --create-channel")
		}
	}
	return id, tgID, err
}

func (a *App) tgChannelID(ctx context.Context) (int64, error) {
	_, tgID, err := a.channelID(ctx)
	if err != nil {
		return 0, err
	}
	var id int64
	_, _ = fmt.Sscanf(tgID, "%d", &id)
	return id, nil
}

func (a *App) activePaths(ctx context.Context, channelID int64) ([]fsmodel.ActivePath, error) {
	rows, err := a.DB.ActivePaths(ctx, channelID)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "list paths", err)
	}
	out := make([]fsmodel.ActivePath, len(rows))
	for i, r := range rows {
		out[i] = fsmodel.ActivePath{Canonical: r.Path, IsDir: r.IsDir}
	}
	return out, nil
}

func computeHash(r io.Reader, enabled bool) (string, error) {
	if !enabled {
		return "", nil
	}
	h := blake3.New(32, nil)
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return "blake3:" + hex.EncodeToString(h.Sum(nil)), nil
}

func detectMIME(path string) string {
	ext := filepath.Ext(path)
	mt := mime.TypeByExtension(ext)
	if mt == "" {
		return "application/octet-stream"
	}
	return mt
}

func (a *App) loadExistingSlugs(ctx context.Context, channelID int64) (map[string]string, error) {
	slugs, err := a.DB.LoadSlugMap(ctx, model.ChannelID(channelID))
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "load slugs", err)
	}
	if slugs == nil {
		return map[string]string{}, nil
	}
	return slugs, nil
}

// loadSlugMap is the best-effort variant of loadExistingSlugs for chain
// generation call sites that treat the persisted cache as advisory.
func (a *App) loadSlugMap(ctx context.Context, channelID int64) map[string]string {
	out, err := a.loadExistingSlugs(ctx, channelID)
	if err != nil {
		return map[string]string{}
	}
	return out
}

func (a *App) uploadLimit(ctx context.Context) int64 {
	a.limitMu.Lock()
	defer a.limitMu.Unlock()
	if a.cachedLimit > 0 {
		return a.cachedLimit
	}
	limit := a.Cfg.Limits.FreeUploadBytes
	if tgChID, err := a.tgChannelID(ctx); err == nil {
		if caps, err := a.TG.Doctor(ctx, tgChID); err == nil && caps != nil && caps.MaxUploadBytes > 0 {
			limit = caps.MaxUploadBytes
		}
	}
	a.cachedLimit = limit
	return limit
}

// UploadFile uploads a single local file.
func (a *App) UploadFile(ctx context.Context, localPath, remotePath string, policy ConflictPolicy, noHash bool) (map[string]any, error) {
	info, err := a.files().Stat(ctx, localPath)
	if err != nil {
		return nil, apperr.New(apperr.ErrLocalNotFound, fmt.Sprintf("local file %q not found", localPath))
	}
	if info.IsDir {
		return nil, apperr.New(apperr.ErrUsage, "use --recursive for directories")
	}
	limit := a.uploadLimit(ctx)
	if info.Size > limit {
		return nil, apperr.New(apperr.ErrFileTooLarge, fmt.Sprintf("file exceeds %d bytes", limit))
	}
	// cp convention: a destination that is "/" or ends with "/" is a
	// directory; keep the source file's basename.
	if remotePath == "/" || strings.HasSuffix(remotePath, "/") {
		remotePath = strings.TrimRight(remotePath, "/") + "/" + filepath.Base(localPath)
	}
	dest, err := fsmodel.NormalizeCanonicalPath(remotePath)
	if err != nil {
		return nil, err
	}
	if dest == "/" {
		return nil, apperr.New(apperr.ErrPathInvalid, "destination must include a file name")
	}
	channelID, tgIDStr, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	tgChID, err := a.tgChannelID(ctx)
	if err != nil {
		return nil, err
	}
	active, err := a.activePaths(ctx, channelID)
	if err != nil {
		return nil, err
	}
	// A destination naming an existing remote directory also keeps the
	// source basename.
	for _, ap := range active {
		if ap.Canonical == dest && ap.IsDir {
			dest, err = fsmodel.NormalizeCanonicalPath(dest + "/" + filepath.Base(localPath))
			if err != nil {
				return nil, err
			}
			break
		}
	}

	// Classify the destination's current occupant. A pending row is a failed
	// or crashed upload, not a live file: it is adoptable on retry (or
	// superseded by --replace) instead of wedging the path.
	var activeExists, pendingAdoptable bool
	err = a.DB.Raw().QueryRowContext(ctx, `
		select exists(select 1 from files where channel_id=? and canonical_path=? and status='active'),
		       exists(select 1 from files where channel_id=? and canonical_path=? and status='pending' and message_id is null)`,
		channelID, dest, channelID, dest).Scan(&activeExists, &pendingAdoptable)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "lookup destination state", err)
	}
	if activeExists && policy != ConflictReplace {
		switch policy {
		case ConflictSkip:
			return map[string]any{"path": dest, "skipped": true}, nil
		case ConflictRename:
			base := fsmodel.BaseName(dest)
			prefix := fsmodel.ParentPath(dest)
			if prefix != "/" {
				prefix += "/"
			}
			for i := 1; i < 1000; i++ {
				candidate, err := fsmodel.NormalizeCanonicalPath(prefix + fsmodel.ConflictRenameCandidate(base, i))
				if err != nil {
					return nil, err
				}
				exists := false
				for _, p := range active {
					if p.Canonical == candidate {
						exists = true
						break
					}
				}
				if !exists {
					dest = candidate
					break
				}
			}
		default:
			return nil, apperr.New(apperr.ErrPathExists,
				fmt.Sprintf("remote file %q already exists (use --replace, --skip-existing, or --auto-rename)", dest))
		}
	}

	// File/dir invariants always apply; the destination itself is excluded
	// when this upload replaces or adopts whatever sits there.
	checkSet := active
	if policy == ConflictReplace || pendingAdoptable {
		checkSet = make([]fsmodel.ActivePath, 0, len(active))
		for _, ap := range active {
			if !ap.IsDir && ap.Canonical == dest {
				continue
			}
			checkSet = append(checkSet, ap)
		}
	}
	if err := fsmodel.CheckUploadConflict(dest, checkSet); err != nil {
		return nil, err
	}

	var replaceFileID int64
	var oldMsgID, oldManifestID sql.NullInt64
	if policy == ConflictReplace {
		switch err := a.DB.Raw().QueryRowContext(ctx, `
			select id, message_id, manifest_message_id from files
			where channel_id=? and canonical_path=? and status='active'`,
			channelID, dest).Scan(&replaceFileID, &oldMsgID, &oldManifestID); err {
		case sql.ErrNoRows:
			replaceFileID = 0
		case nil:
		default:
			return nil, apperr.Wrap(apperr.ErrDB, "lookup replace target", err)
		}
	}

	var data map[string]any
	lockErr := a.withLocks(ctx, []string{sqlitestore.LockKey(channelID, dest)}, func(ctx context.Context) error {
		var err error
		// Hashing is required, not optional, on the resumable path: resume
		// identity (and therefore byte-exactness) can never rest on an empty
		// hash. --no-hash is honored only for small files.
		hashEnabled := a.Cfg.Hash.Enabled && !noHash
		if info.Size > telegram.ResumableBigFileBytes {
			hashEnabled = true
		}
		data, err = a.uploadLocked(ctx, uploadLockedArgs{
			localPath:     localPath,
			dest:          dest,
			policy:        policy,
			hashEnabled:   hashEnabled,
			size:          info.Size,
			channelID:     channelID,
			tgChID:        tgChID,
			tgIDStr:       tgIDStr,
			replaceFileID: replaceFileID,
			oldMsgID:      oldMsgID,
			oldManifestID: oldManifestID,
		})
		return err
	})
	if lockErr != nil {
		return nil, lockErr
	}
	return data, nil
}

type uploadLockedArgs struct {
	localPath     string
	dest          string
	policy        ConflictPolicy
	hashEnabled   bool
	size          int64
	channelID     int64
	tgChID        int64
	tgIDStr       string
	replaceFileID int64
	oldMsgID      sql.NullInt64
	oldManifestID sql.NullInt64
}

func (a *App) uploadLocked(ctx context.Context, args uploadLockedArgs) (map[string]any, error) {
	dest, localPath, channelID, tgChID, tgIDStr := args.dest, args.localPath, args.channelID, args.tgChID, args.tgIDStr
	now := time.Now().UTC().Format(time.RFC3339)
	hashReader, err := a.files().Open(ctx, localPath)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrLocalNotFound, "hash file", err)
	}
	contentHash, err := computeHash(hashReader, args.hashEnabled)
	_ = hashReader.Close()
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrLocalNotFound, "hash file", err)
	}
	size := args.size
	bigFile := size > telegram.ResumableBigFileBytes

	// Resolve the pending row under the lock, now that the content identity
	// is known: adopt on match (plain retry resumes the interrupted parts),
	// supersede under --replace, block otherwise.
	type pendingRow struct {
		id    int64
		size  sql.NullInt64
		hash  sql.NullString
		msgID sql.NullInt64
	}
	var pending pendingRow
	var pendingExists bool
	err = a.DB.Raw().QueryRowContext(ctx, `
		select id, size, content_hash, message_id from files
		where channel_id=? and canonical_path=? and status='pending'`,
		channelID, dest).Scan(&pending.id, &pending.size, &pending.hash, &pending.msgID)
	switch err {
	case sql.ErrNoRows:
	case nil:
		pendingExists = true
	default:
		return nil, apperr.Wrap(apperr.ErrDB, "lookup pending destination", err)
	}
	adoptFileID := int64(0)
	resumed := false
	if pendingExists {
		switch {
		case args.policy == ConflictReplace:
			// Supersede the pending row and its stale upload state; roll back
			// a media message a crash left recorded but unpublished.
			if pending.msgID.Valid {
				_ = a.TG.DeleteMessage(ctx, tgChID, int(pending.msgID.Int64))
			}
			if err := a.DB.DeleteUploadStateByFile(ctx, pending.id); err != nil {
				return nil, apperr.Wrap(apperr.ErrDB, "clear superseded upload state", err)
			}
			if _, err := a.DB.Raw().ExecContext(ctx, `delete from files where id=?`, pending.id); err != nil {
				return nil, apperr.Wrap(apperr.ErrDB, "supersede pending row", err)
			}
		case pending.msgID.Valid:
			// Crash window: the media exists on Telegram but was never
			// published. Re-uploading here would duplicate it.
			return nil, apperr.New(apperr.ErrPathExists,
				fmt.Sprintf("an unpublished upload of %q is waiting at message %d; run: td repair --orphaned", dest, pending.msgID.Int64))
		default:
			sizeMatch := pending.size.Valid && pending.size.Int64 == size
			hashMatch := !pending.hash.Valid || pending.hash.String == "" || pending.hash.String == contentHash
			if sizeMatch && hashMatch {
				// Adopt the pending row: reusing its id reuses its resumable
				// state key, so only unconfirmed parts are re-sent.
				adoptFileID = pending.id
			} else {
				return nil, apperr.New(apperr.ErrPathExists,
					fmt.Sprintf("an interrupted upload of different content occupies %q (use --replace to supersede it)", dest))
			}
		}
	}

	existingSlugs, err := a.loadExistingSlugs(ctx, channelID)
	if err != nil {
		return nil, err
	}
	// Render the caption exactly once and thread it into the publish step, so
	// the caption on Telegram and the tags in the index cannot diverge.
	displayName := fsmodel.BaseName(dest)
	meta := manifest.FileMeta{
		CanonicalPath: dest,
		DisplayName:   displayName,
		ParentHuman:   fsmodel.HumanParent(dest),
		Size:          size,
		Hash:          contentHash,
		MIME:          detectMIME(localPath),
		Created:       now,
	}
	tags, slugMaps, err := pathcodec.GenerateChain(dest, existingSlugs)
	if err != nil {
		return nil, err
	}
	meta.Tags = tags
	capRes, err := manifest.RenderCaption(meta, a.Cfg.Caption.SafeMediaCaptionUTF16Units, a.Cfg.Caption.MarginUTF16Units)
	if err != nil {
		return nil, err
	}

	var fileID int64
	if adoptFileID > 0 {
		fileID = adoptFileID
		if _, err := a.DB.Raw().ExecContext(ctx, `
			update files set display_name=?, original_local_path=?, size=?, content_hash=?, mime=?, message_id=null, updated_at=? where id=?`,
			displayName, localPath, size, contentHash, meta.MIME, now, fileID); err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "adopt pending row", err)
		}
		if st, lerr := a.DB.LoadUploadState(ctx, fmt.Sprintf("file:%d", fileID)); lerr == nil && st != nil && st.ConfirmedBytes > 0 {
			resumed = true
		}
	} else {
		// Always insert a fresh pending row. Under --replace the old active row
		// stays untouched until the new upload fully succeeds, so a failed
		// upload can never lose the existing index entry.
		err = a.DB.WithTx(ctx, func(tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, `insert into files(channel_id,canonical_path,display_name,original_local_path,size,content_hash,mime,status,updated_at) values(?,?,?,?,?,?,?,'pending',?)`,
				channelID, dest, displayName, localPath, size, contentHash, meta.MIME, now)
			if err != nil {
				return err
			}
			fileID, _ = res.LastInsertId()
			return nil
		})
		if err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "insert pending", err)
		}
	}

	threads := a.Cfg.Upload.Threads
	if threads <= 0 {
		threads = 4
	}
	partSize := a.Cfg.Upload.PartSizeKB * 1024
	req := telegram.UploadRequest{
		ChannelID:      tgChID,
		Caption:        capRes.Caption,
		FileName:       displayName,
		MIME:           meta.MIME,
		Size:           size,
		ContentHash:    contentHash,
		Path:           localPath,
		Threads:        threads,
		PartSize:       partSize,
		ResumableKey:   fmt.Sprintf("file:%d", fileID),
		ResumableStore: a.DB,
	}
	if !bigFile {
		// Small files stream from a reader; the resumable path reads by offset
		// and never holds a reader for the upload's duration.
		f, err := a.files().Open(ctx, localPath)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		req.Reader = f
	}
	if a.Progress != nil {
		req.Progress = a.Progress
	}
	up, err := a.TG.UploadMedia(ctx, req)
	if err != nil {
		// Keep pending state only for resumable big uploads; small files and
		// permission errors do not benefit from resuming.
		if !bigFile {
			_, _ = a.DB.Raw().ExecContext(ctx, `delete from files where id=?`, fileID)
		}
		return nil, telegram.MapError(err)
	}
	// Persist message_id before any further Telegram or DB work so a crash
	// or index failure cannot look like "never uploaded" to RepairPending.
	if recErr := a.recordPendingMessage(ctx, fileID, up.MessageID, now); recErr != nil {
		if a.abandonUploadedMedia(ctx, tgChID, fileID, up.MessageID, now) {
			return nil, apperr.New(apperr.ErrOrphanedUpload, fmt.Sprintf("upload reached Telegram message %d but could not be recorded; run td repair --orphaned", up.MessageID))
		}
		return nil, recErr
	}

	pubRes, pubErr := a.publisher().Publish(ctx, publisher.PublishRequest{
		ChannelRowID:  channelID,
		ChannelID:     tgChID,
		FileID:        fileID,
		MessageID:     up.MessageID,
		Meta:          meta,
		ExistingSlugs: existingSlugs,
		SetUploadedAt: true,
		ReplaceFileID: args.replaceFileID,
		Rendered:      &capRes,
		Tags:          tags,
		SlugMaps:      slugMaps,
	})
	if pubErr != nil {
		if a.abandonUploadedMedia(ctx, tgChID, fileID, up.MessageID, now) {
			return nil, apperr.New(apperr.ErrOrphanedUpload, fmt.Sprintf("upload reached Telegram message %d but could not be completed or rolled back; run td repair --orphaned", up.MessageID))
		}
		return nil, pubErr
	}

	var manifestMsgID *int
	if pubRes.ManifestMsgID > 0 {
		manifestMsgID = &pubRes.ManifestMsgID
	}

	// Clean up the replaced Telegram message only after the DB state
	// committed; best effort — the old row is already superseded. In
	// tombstone mode the old message must be redacted, never left claiming
	// the path with its original content.
	if args.replaceFileID > 0 {
		if a.Cfg.Delete.Mode == "tombstone" {
			if args.oldMsgID.Valid {
				_ = a.TG.EditCaption(ctx, tgChID, int(args.oldMsgID.Int64), manifest.RenderTombstoneCaption(displayName, dest))
			}
			if args.oldManifestID.Valid {
				_ = a.TG.EditText(ctx, tgChID, int(args.oldManifestID.Int64), manifest.RenderTombstoneManifest(dest))
			}
		} else {
			if args.oldMsgID.Valid {
				_ = a.TG.DeleteMessage(ctx, tgChID, int(args.oldMsgID.Int64))
			}
			if args.oldManifestID.Valid {
				_ = a.TG.DeleteMessage(ctx, tgChID, int(args.oldManifestID.Int64))
			}
		}
	}

	data := map[string]any{
		"path":                dest,
		"channel_id":          tgIDStr,
		"message_id":          up.MessageID,
		"manifest_message_id": manifestMsgID,
		"size":                size,
		"hash":                contentHash,
	}
	if resumed {
		data["resumed"] = true
	}
	if link, err := a.TG.GetInviteLink(ctx, tgChID); err == nil && link != "" {
		data["invite_link"] = link
	}
	return data, nil
}
