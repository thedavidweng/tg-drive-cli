package service

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/pathcodec"
	"github.com/thedavidweng/tg-drive-cli/core/publisher"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// Album uploads publish several files as native Telegram media groups
// (ADR 0013 / issue #26): one human td:v1 caption on the very first member,
// empty sibling captions, and one td-album:v1 inventory reply per group.
// Sets larger than Telegram's group limit split into consecutive groups of
// MaxMediaGroupMembers.

// albumSource pairs a local file with its resolved remote destination.
type albumSource struct {
	localPath string
	dest      string
}

// albumMember is a fully planned batch member: hashed, caption-rendered,
// and backed by a pending index row.
type albumMember struct {
	src      albumSource
	size     int64
	hash     string
	mime     string
	fileID   int64
	adopted  bool
	resumed  bool
	bigFile  bool
	meta     manifest.FileMeta
	capRes   manifest.CaptionResult
	tags     []string
	slugMaps []pathcodec.SlugMapping
}

// AlbumGroup describes one sent media group for the JSON envelope.
type AlbumGroup struct {
	GroupedID      int64    `json:"grouped_id"`
	ReplyMessageID int      `json:"reply_message_id"`
	MessageIDs     []int    `json:"message_ids"`
	Paths          []string `json:"paths"`
}

// albumBatch is the outcome of the planning phase plus everything the send
// phase needs to build requests.
type albumBatch struct {
	members []*albumMember
	pres    Presentation
	skipped int
}

// UploadFilesAs uploads multiple local files as native Telegram albums
// sharing one destination directory. The zero Presentation behaves like the
// document default; presentation flags apply uniformly to every member.
// Conflict policies apply per file; --replace is not supported (replace
// individual files with single-path td cp instead).
func (a *App) UploadFilesAs(ctx context.Context, localPaths []string, remoteDir string, policy ConflictPolicy, noHash bool, pres Presentation) (map[string]any, error) {
	if len(localPaths) < 2 {
		return nil, apperr.New(apperr.ErrUsage, "album upload requires at least two local files")
	}
	if err := pres.Validate(); err != nil {
		return nil, err
	}
	if policy == ConflictReplace {
		return nil, apperr.New(apperr.ErrUsage,
			"album uploads cannot --replace; replace an existing file with single-path td cp --replace, or remove it first")
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
	dir, err := albumDestinationDir(remoteDir, active)
	if err != nil {
		return nil, err
	}

	var sources []albumSource
	seen := map[string]bool{}
	for _, lp := range localPaths {
		dest, err := fsmodel.NormalizeCanonicalPath(dir + "/" + filepath.Base(lp))
		if err != nil {
			return nil, err
		}
		if seen[dest] {
			return nil, apperr.New(apperr.ErrUsage,
				fmt.Sprintf("two source files map to %q; album members need distinct basenames", dest))
		}
		seen[dest] = true
		sources = append(sources, albumSource{localPath: lp, dest: dest})
	}

	batch, _, err := a.planAlbumBatch(ctx, sources, policy, noHash, pres, false)
	if err != nil {
		return nil, err
	}
	data, err := a.runAlbumBatch(ctx, batch, channelID, tgChID, tgIDStr)
	if err != nil {
		return nil, err
	}
	data["skipped"] = batch.skipped
	return data, nil
}

// albumDestinationDir validates the multi-file destination and returns its
// canonical form: "/" anywhere, a path with a trailing slash (directory
// declared), or an existing remote directory named without one. The canonical
// path never carries a trailing slash — fsmodel rejects those.
func albumDestinationDir(remoteDir string, active []fsmodel.ActivePath) (string, error) {
	trimmed := strings.TrimRight(remoteDir, "/")
	if trimmed == "" {
		return "/", nil
	}
	p, err := fsmodel.NormalizeCanonicalPath(trimmed)
	if err != nil {
		return "", err
	}
	if trimmed == remoteDir {
		// No trailing slash: the target must already exist as a remote dir,
		// so a meant-to-be-single-file destination cannot be silently
		// reinterpreted.
		isDir := false
		for _, ap := range active {
			if ap.IsDir && ap.Canonical == p {
				isDir = true
				break
			}
		}
		if !isDir {
			return "", apperr.New(apperr.ErrUsage,
				fmt.Sprintf("multi-file destination %q must be a directory (end the path with /)", remoteDir))
		}
	}
	return p, nil
}

// planAlbumBatch resolves every source into a hashed, captioned member backed
// by a pending index row. Nothing touches Telegram. In strict mode (lenient
// false) the first per-source failure aborts and rolls back freshly inserted
// pending rows; in lenient mode (--continue-on-error) failures are returned
// alongside the surviving batch so the caller can report them.
func (a *App) planAlbumBatch(ctx context.Context, sources []albumSource, policy ConflictPolicy, noHash bool, pres Presentation, lenient bool) (*albumBatch, []string, error) {
	channelID, _, err := a.channelID(ctx)
	if err != nil {
		return nil, nil, err
	}
	active, err := a.activePaths(ctx, channelID)
	if err != nil {
		return nil, nil, err
	}
	limit := a.uploadLimit(ctx)
	now := time.Now().UTC().Format(time.RFC3339)
	existingSlugs, err := a.loadExistingSlugs(ctx, channelID)
	if err != nil {
		return nil, nil, err
	}

	batch := &albumBatch{pres: pres}
	var failures []string
	var freshRows []int64 // pending rows inserted here, rolled back on fatal
	fail := func(err error) (*albumBatch, []string, error) {
		for _, id := range freshRows {
			_, _ = a.DB.Raw().ExecContext(ctx, `delete from files where id=?`, id)
		}
		return nil, nil, err
	}
	for _, src := range sources {
		member, err := a.planAlbumMember(ctx, channelID, src, policy, noHash, now, existingSlugs, active, limit)
		if err != nil {
			if !lenient {
				return fail(err)
			}
			failures = append(failures, fmt.Sprintf("%s: %v", src.localPath, err))
			continue
		}
		if member == nil {
			batch.skipped++
			continue
		}
		if !member.adopted {
			freshRows = append(freshRows, member.fileID)
		}
		batch.members = append(batch.members, member)
	}
	return batch, failures, nil
}

// planAlbumMember stages one source. A nil member with a nil error means the
// file was dropped by --skip-existing.
func (a *App) planAlbumMember(ctx context.Context, channelRowID int64, src albumSource, policy ConflictPolicy, noHash bool, now string, existingSlugs map[string]string, active []fsmodel.ActivePath, limit int64) (*albumMember, error) {
	info, err := a.files().Stat(ctx, src.localPath)
	if err != nil {
		return nil, apperr.New(apperr.ErrLocalNotFound, fmt.Sprintf("local file %q not found", src.localPath))
	}
	if info.IsDir {
		return nil, apperr.New(apperr.ErrUsage, fmt.Sprintf("%q is a directory; album members must be files", src.localPath))
	}
	if info.Size > limit {
		return nil, apperr.New(apperr.ErrFileTooLarge, fmt.Sprintf("%s exceeds %d bytes", src.dest, limit))
	}

	// Classify the destination occupant exactly like the single-file path.
	var activeExists, pendingAdoptable bool
	err = a.DB.Raw().QueryRowContext(ctx, `
		select exists(select 1 from files where channel_id=? and canonical_path=? and status='active'),
		       exists(select 1 from files where channel_id=? and canonical_path=? and status='pending' and message_id is null)`,
		channelRowID, src.dest, channelRowID, src.dest).Scan(&activeExists, &pendingAdoptable)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "lookup destination state", err)
	}
	dest := src.dest
	if activeExists {
		switch policy {
		case ConflictSkip:
			return nil, nil
		case ConflictRename:
			base := fsmodel.BaseName(dest)
			prefix := fsmodel.ParentPath(dest)
			if prefix != "/" {
				prefix += "/"
			}
			renamed := false
			for i := 1; i < 1000; i++ {
				candidate, err := fsmodel.NormalizeCanonicalPath(prefix + fsmodel.ConflictRenameCandidate(base, i))
				if err != nil {
					return nil, err
				}
				taken := false
				for _, ap := range active {
					if ap.Canonical == candidate {
						taken = true
						break
					}
				}
				if !taken {
					dest = candidate
					renamed = true
					break
				}
			}
			if !renamed {
				return nil, apperr.New(apperr.ErrPathExists, fmt.Sprintf("no free auto-rename candidate for %q", dest))
			}
			src.dest = dest
		default:
			return nil, apperr.New(apperr.ErrPathExists,
				fmt.Sprintf("remote file %q already exists (use --skip-existing or --auto-rename)", dest))
		}
	}
	checkSet := make([]fsmodel.ActivePath, 0, len(active)+1)
	for _, ap := range active {
		if !ap.IsDir && ap.Canonical == dest && (activeExists || pendingAdoptable) {
			continue
		}
		checkSet = append(checkSet, ap)
	}
	if err := fsmodel.CheckUploadConflict(dest, checkSet); err != nil {
		return nil, err
	}

	// Hashing mirrors the single-file rule: required for resumable big files,
	// optional otherwise.
	hashEnabled := a.Cfg.Hash.Enabled && !noHash
	if info.Size > telegram.ResumableBigFileBytes {
		hashEnabled = true
	}
	hashReader, err := a.files().Open(ctx, src.localPath)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrLocalNotFound, "hash file", err)
	}
	contentHash, err := computeHash(hashReader, hashEnabled)
	_ = hashReader.Close()
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrLocalNotFound, "hash file", err)
	}

	displayName := fsmodel.BaseName(dest)
	meta := manifest.FileMeta{
		CanonicalPath: dest,
		DisplayName:   displayName,
		ParentHuman:   fsmodel.HumanParent(dest),
		Size:          info.Size,
		Hash:          contentHash,
		MIME:          detectMIME(src.localPath),
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

	// Pending-row reconciliation, mirroring uploadLocked: adopt matching
	// interrupted uploads, refuse blocked ones.
	adoptFileID := int64(0)
	var pendingMsgID sql.NullInt64
	var pendingSize sql.NullInt64
	var pendingHash sql.NullString
	switch err := a.DB.Raw().QueryRowContext(ctx, `
		select id, message_id, size, content_hash from files
		where channel_id=? and canonical_path=? and status='pending'`,
		channelRowID, dest).Scan(&adoptFileID, &pendingMsgID, &pendingSize, &pendingHash); err {
	case sql.ErrNoRows:
		adoptFileID = 0
	case nil:
	default:
		return nil, apperr.Wrap(apperr.ErrDB, "lookup pending destination", err)
	}
	if adoptFileID > 0 {
		if pendingMsgID.Valid {
			return nil, apperr.New(apperr.ErrPathExists,
				fmt.Sprintf("an unpublished upload of %q is waiting at message %d; run: td repair --orphaned", dest, pendingMsgID.Int64))
		}
		sizeMatch := pendingSize.Valid && pendingSize.Int64 == info.Size
		hashMatch := !pendingHash.Valid || pendingHash.String == "" || pendingHash.String == contentHash
		if !sizeMatch || !hashMatch {
			return nil, apperr.New(apperr.ErrPathExists,
				fmt.Sprintf("an interrupted upload of different content occupies %q; replace it with single-path td cp --replace or remove it first", dest))
		}
	}

	var fileID int64
	resumed := false
	if adoptFileID > 0 {
		fileID = adoptFileID
		if _, err := a.DB.Raw().ExecContext(ctx, `
			update files set display_name=?, original_local_path=?, size=?, content_hash=?, mime=?, message_id=null, updated_at=? where id=?`,
			displayName, src.localPath, info.Size, contentHash, meta.MIME, now, fileID); err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "adopt pending row", err)
		}
		if st, lerr := a.DB.LoadUploadState(ctx, fmt.Sprintf("file:%d", fileID)); lerr == nil && st != nil && st.ConfirmedBytes > 0 {
			resumed = true
		}
	} else {
		res, err := a.DB.Raw().ExecContext(ctx, `insert into files(channel_id,canonical_path,display_name,original_local_path,size,content_hash,mime,status,updated_at) values(?,?,?,?,?,?,?,'pending',?)`,
			channelRowID, dest, displayName, src.localPath, info.Size, contentHash, meta.MIME, now)
		if err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "insert pending", err)
		}
		fileID, _ = res.LastInsertId()
	}

	return &albumMember{
		src:      albumSource{localPath: src.localPath, dest: dest},
		size:     info.Size,
		hash:     contentHash,
		mime:     meta.MIME,
		fileID:   fileID,
		adopted:  adoptFileID > 0,
		resumed:  resumed,
		bigFile:  info.Size > telegram.ResumableBigFileBytes,
		meta:     meta,
		capRes:   capRes,
		tags:     tags,
		slugMaps: slugMaps,
	}, nil
}

// runAlbumBatch sends the batch in chunks of MaxMediaGroupMembers and
// completes each group's publication: message ids recorded, one td-album:v1
// inventory reply, index rows activated. Every destination path is locked for
// the whole batch so concurrent mutators cannot interleave.
func (a *App) runAlbumBatch(ctx context.Context, batch *albumBatch, channelID, tgChID int64, tgIDStr string) (map[string]any, error) {
	dests := make([]string, 0, len(batch.members))
	for _, m := range batch.members {
		dests = append(dests, m.src.dest)
	}
	var groups []AlbumGroup
	resumedAny := false
	sent := 0
	lockErr := a.withLocks(ctx, lockKeysForPaths(channelID, dests...), func(ctx context.Context) error {
		groups = []AlbumGroup{}
		sent = 0
		threads := a.Cfg.Upload.Threads
		if threads <= 0 {
			threads = 4
		}
		partSize := a.Cfg.Upload.PartSizeKB * 1024
		var thumb []byte
		if batch.pres.ThumbPath != "" {
			tf, err := a.files().Open(ctx, batch.pres.ThumbPath)
			if err != nil {
				return apperr.Wrap(apperr.ErrLocalNotFound, "open thumbnail", err)
			}
			thumb, err = io.ReadAll(tf)
			_ = tf.Close()
			if err != nil {
				return apperr.Wrap(apperr.ErrLocalNotFound, "read thumbnail", err)
			}
		}

		for start := 0; start < len(batch.members); start += telegram.MaxMediaGroupMembers {
			end := start + telegram.MaxMediaGroupMembers
			if end > len(batch.members) {
				end = len(batch.members)
			}
			chunk := batch.members[start:end]
			// One human caption per batch, on the very first member; later
			// chunks and siblings stay empty.
			withCaption := start == 0
			if len(chunk) == 1 {
				// A Telegram media group holds at least two members; a lone
				// survivor (a one-file directory, or every sibling skipped)
				// uploads as an ordinary single message.
				if _, err := a.sendSingleAlbumMember(ctx, chunk[0], channelID, tgChID, batch.pres, threads, partSize, thumb); err != nil {
					return err
				}
				sent++
				if chunk[0].resumed {
					resumedAny = true
				}
				continue
			}
			group, err := a.sendAlbumChunk(ctx, chunk, channelID, tgChID, batch.pres, withCaption, threads, partSize, thumb)
			if err != nil {
				return err
			}
			groups = append(groups, *group)
			sent += len(chunk)
			for _, m := range chunk {
				if m.resumed {
					resumedAny = true
				}
			}
		}
		return nil
	})
	if lockErr != nil {
		return nil, lockErr
	}

	out := map[string]any{
		"uploaded":   sent,
		"errors":     []string{},
		"albums":     groups,
		"channel_id": tgIDStr,
	}
	if resumedAny {
		out["resumed"] = true
	}
	if link, err := a.TG.GetInviteLink(ctx, tgChID); err == nil && link != "" {
		out["invite_link"] = link
	}
	return out, nil
}

// sendSingleAlbumMember publishes one planned member as an ordinary single
// message with its own td:v1 caption (and per-file manifest reply when the
// caption budget demands one) — the exact single-upload semantics.
func (a *App) sendSingleAlbumMember(ctx context.Context, m *albumMember, channelID, tgChID int64, pres Presentation, threads, partSize int, thumb []byte) (int, error) {
	req := telegram.UploadRequest{
		ChannelID:      tgChID,
		Caption:        m.capRes.Caption,
		FileName:       m.meta.DisplayName,
		MIME:           m.mime,
		Size:           m.size,
		ContentHash:    m.hash,
		Path:           m.src.localPath,
		Threads:        threads,
		PartSize:       partSize,
		ResumableKey:   fmt.Sprintf("file:%d", m.fileID),
		ResumableStore: a.DB,
	}
	pres.apply(&req)
	if len(thumb) > 0 && pres.Kind != telegram.KindPhoto {
		req.Thumb = thumb
	}
	if !m.bigFile {
		f, err := a.files().Open(ctx, m.src.localPath)
		if err != nil {
			return 0, err
		}
		defer func() { _ = f.Close() }()
		req.Reader = f
	}
	if a.Progress != nil {
		req.Progress = a.Progress
	}
	up, err := a.TG.UploadMedia(ctx, req)
	if err != nil {
		a.cleanupUnsentChunk(ctx, []*albumMember{m})
		return 0, telegram.MapError(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if recErr := a.recordPendingMessage(ctx, m.fileID, up.MessageID, now); recErr != nil {
		if a.abandonUploadedMedia(ctx, tgChID, m.fileID, up.MessageID, now) {
			return 0, apperr.New(apperr.ErrOrphanedUpload,
				fmt.Sprintf("upload of %q reached Telegram but could not be recorded; run td repair --orphaned", m.src.dest))
		}
		return 0, recErr
	}
	existingSlugs := a.loadSlugMap(ctx, channelID)
	if _, pubErr := a.publisher().Publish(ctx, publisher.PublishRequest{
		ChannelRowID:  channelID,
		ChannelID:     tgChID,
		FileID:        m.fileID,
		MessageID:     up.MessageID,
		Meta:          m.meta,
		ExistingSlugs: existingSlugs,
		SetUploadedAt: true,
		Rendered:      &m.capRes,
		Tags:          m.tags,
		SlugMaps:      m.slugMaps,
	}); pubErr != nil {
		if a.abandonUploadedMedia(ctx, tgChID, m.fileID, up.MessageID, now) {
			return 0, apperr.New(apperr.ErrOrphanedUpload,
				fmt.Sprintf("upload of %q reached Telegram but could not be completed or rolled back; run td repair --orphaned", m.src.dest))
		}
		return 0, pubErr
	}
	return up.MessageID, nil
}

// sendAlbumChunk sends one ≤MaxMediaGroupMembers media group and completes
// its publication. withCaption gates the batch's single human caption (the
// first member of the first chunk only). Any failure abandons the whole
// chunk (messages deleted, rows cleaned or orphaned), mirroring the
// single-upload crash windows.
func (a *App) sendAlbumChunk(ctx context.Context, chunk []*albumMember, channelID, tgChID int64, pres Presentation, withCaption bool, threads, partSize int, thumb []byte) (*AlbumGroup, error) {
	reqs := make([]telegram.UploadRequest, 0, len(chunk))
	var readers []io.Closer
	defer func() {
		for _, r := range readers {
			_ = r.Close()
		}
	}()
	for i, m := range chunk {
		req := telegram.UploadRequest{
			ChannelID:      tgChID,
			FileName:       m.meta.DisplayName,
			MIME:           m.mime,
			Size:           m.size,
			ContentHash:    m.hash,
			Path:           m.src.localPath,
			Threads:        threads,
			PartSize:       partSize,
			ResumableKey:   fmt.Sprintf("file:%d", m.fileID),
			ResumableStore: a.DB,
		}
		if i == 0 && withCaption {
			// One human caption per batch: the first member of the first
			// group carries it; sibling captions stay empty (ADR 0013).
			req.Caption = m.capRes.Caption
		}
		if len(thumb) > 0 && pres.Kind != telegram.KindPhoto {
			req.Thumb = thumb
		}
		if !m.bigFile {
			f, err := a.files().Open(ctx, m.src.localPath)
			if err != nil {
				return nil, err
			}
			readers = append(readers, f)
			req.Reader = f
		}
		if a.Progress != nil {
			req.Progress = a.Progress
		}
		pres.apply(&req)
		reqs = append(reqs, req)
	}

	results, err := a.TG.UploadMediaGroup(ctx, reqs)
	if err != nil {
		a.cleanupUnsentChunk(ctx, chunk)
		return nil, telegram.MapError(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for i, res := range results {
		if recErr := a.recordPendingMessage(ctx, chunk[i].fileID, res.MessageID, now); recErr != nil {
			a.abandonAlbumChunk(ctx, tgChID, chunk, results, 0, now)
			return nil, apperr.New(apperr.ErrOrphanedUpload,
				"album reached Telegram but could not be recorded; run td repair --orphaned")
		}
	}

	meta := manifest.AlbumMeta{GroupedID: results[0].GroupedID}
	for i, res := range results {
		meta.Files = append(meta.Files, manifest.AlbumFileFromMeta(res.MessageID, manifest.FileMeta{
			CanonicalPath: chunk[i].src.dest,
			DisplayName:   chunk[i].meta.DisplayName,
			Size:          chunk[i].size,
			Hash:          chunk[i].hash,
			MIME:          chunk[i].mime,
		}))
	}
	replyID, err := a.writeAlbumManifest(ctx, channelID, tgChID, 0, results[0].MessageID, meta)
	if err != nil {
		a.abandonAlbumChunk(ctx, tgChID, chunk, results, 0, now)
		return nil, err
	}

	existingSlugs := a.loadSlugMap(ctx, channelID)
	for i, res := range results {
		m := chunk[i]
		_, pubErr := a.publisher().Publish(ctx, publisher.PublishRequest{
			ChannelRowID:      channelID,
			ChannelID:         tgChID,
			FileID:            m.fileID,
			MessageID:         res.MessageID,
			ManifestMsgID:     replyID,
			SkipManifestReply: true,
			Meta:              m.meta,
			ExistingSlugs:     existingSlugs,
			SetUploadedAt:     true,
			Rendered:          &m.capRes,
			Tags:              m.tags,
			SlugMaps:          m.slugMaps,
		})
		if pubErr != nil {
			a.abandonAlbumChunk(ctx, tgChID, chunk, results, replyID, now)
			if a.isAbandonWindow(pubErr) {
				return nil, apperr.New(apperr.ErrOrphanedUpload,
					"album reached Telegram but could not be completed or rolled back; run td repair --orphaned")
			}
			return nil, pubErr
		}
	}

	paths := make([]string, 0, len(results))
	ids := make([]int, 0, len(results))
	for i, res := range results {
		paths = append(paths, chunk[i].src.dest)
		ids = append(ids, res.MessageID)
	}
	return &AlbumGroup{GroupedID: results[0].GroupedID, ReplyMessageID: replyID, MessageIDs: ids, Paths: paths}, nil
}

// isAbandonWindow reports whether a publish failure means the media may be
// stranded on Telegram (the single-upload abandonUploadedMedia criterion).
func (a *App) isAbandonWindow(err error) bool {
	if ae, ok := apperr.As(err); ok {
		return ae.Code == apperr.ErrDB || ae.Code == apperr.ErrTelegramRPC
	}
	return false
}

// cleanupUnsentChunk drops the pending rows of members whose group never
// reached Telegram. Resumable big-file rows survive for retry, matching the
// single-upload failure policy.
func (a *App) cleanupUnsentChunk(ctx context.Context, chunk []*albumMember) {
	for _, m := range chunk {
		if !m.bigFile {
			_, _ = a.DB.Raw().ExecContext(ctx, `delete from files where id=?`, m.fileID)
		}
	}
}

// abandonAlbumChunk rolls back a chunk that reached Telegram but could not be
// published: messages (and the inventory reply) are deleted when possible and
// pending rows cleaned up; rows that cannot be proven deleted stay orphaned
// with their message ids so RepairPending/RepairOrphaned reconcile them.
func (a *App) abandonAlbumChunk(ctx context.Context, tgChID int64, chunk []*albumMember, results []telegram.UploadResult, replyID int, now string) {
	if replyID > 0 {
		_ = a.TG.DeleteMessage(ctx, tgChID, replyID)
	}
	for i, res := range results {
		m := chunk[i]
		delErr := a.TG.DeleteMessage(ctx, tgChID, res.MessageID)
		if delErr == nil || isMessageGone(delErr) {
			_, _ = a.DB.Raw().ExecContext(ctx, `delete from files where id=?`, m.fileID)
			continue
		}
		_, _ = a.DB.Raw().ExecContext(ctx,
			`update files set status='orphaned', message_id=?, updated_at=? where id=?`,
			res.MessageID, now, m.fileID)
	}
}
