package service

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/pathcodec"
	"github.com/thedavidweng/tg-drive-cli/core/ports"
	"lukechampine.com/blake3"
)

// LSEntry is one directory listing entry.
type LSEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Type      string `json:"type"`
	Size      int64  `json:"size,omitempty"`
	Status    string `json:"status,omitempty"`
	Ephemeral bool   `json:"ephemeral,omitempty"`
}

// ListDir lists children of a remote path.
func (a *App) ListDir(ctx context.Context, remotePath string) ([]LSEntry, error) {
	p, err := fsmodel.NormalizeCanonicalPath(remotePath)
	if err != nil {
		return nil, err
	}
	channelID, _, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	prefix := p
	if prefix != "/" {
		prefix += "/"
	}
	rows, err := a.DB.Raw().QueryContext(ctx, `
		select canonical_path, display_name, 'file' as type, coalesce(size,0), status, 0
		from files where channel_id=? and status='active' and canonical_path like ? escape '\'
		union
		select canonical_path, display_name, 'dir', 0, '', ephemeral
		from nodes where channel_id=? and type='dir' and parent_path=?
		order by type desc, display_name`, channelID, prefix+"%", channelID, p)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "ls", err)
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]bool{}
	var out []LSEntry
	for rows.Next() {
		var fullPath, name, typ, status string
		var size int64
		var ephemeral int
		if err := rows.Scan(&fullPath, &name, &typ, &size, &status, &ephemeral); err != nil {
			return nil, err
		}
		childName := name
		if typ == "file" {
			rel := strings.TrimPrefix(fullPath, prefix)
			if strings.Contains(rel, "/") {
				childName = strings.Split(rel, "/")[0]
				typ = "dir"
				fullPath = prefix + childName
			} else {
				childName = name
			}
		}
		if seen[fullPath] {
			continue
		}
		seen[fullPath] = true
		out = append(out, LSEntry{Name: childName, Path: fullPath, Type: typ, Size: size, Status: status, Ephemeral: ephemeral == 1})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type == "dir"
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// TreeNode is a tree entry.
type TreeNode struct {
	Path     string     `json:"path"`
	Name     string     `json:"name"`
	Type     string     `json:"type"`
	Children []TreeNode `json:"children,omitempty"`
}

// Tree builds a directory tree up to maxDepth.
func (a *App) Tree(ctx context.Context, remotePath string, maxDepth int) ([]TreeNode, error) {
	entries, err := a.ListDir(ctx, remotePath)
	if err != nil {
		return nil, err
	}
	if maxDepth == 0 {
		maxDepth = 32
	}
	var nodes []TreeNode
	for _, e := range entries {
		node := TreeNode{Path: e.Path, Name: e.Name, Type: e.Type}
		if e.Type == "dir" && maxDepth > 1 {
			children, err := a.Tree(ctx, e.Path, maxDepth-1)
			if err != nil {
				return nil, err
			}
			node.Children = children
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// Status returns index statistics.
func (a *App) Status(ctx context.Context) (map[string]any, error) {
	channelID, tgID, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	rows, err := a.DB.Raw().QueryContext(ctx, `select status, count(*) from files where channel_id=? group by status`, channelID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var status string
		var n int
		_ = rows.Scan(&status, &n)
		counts[status] = n
	}
	out := map[string]any{
		"channel_id": tgID,
		"files":      counts,
		"db_path":    a.Cfg.Storage.DBPath,
	}
	user, ok, err := a.TG.Status(ctx)
	if err == nil {
		out["authenticated"] = ok
		if ok && user != nil {
			out["user_id"] = user.ID
		}
	}
	var lastScan, lastFull string
	var lastMsgID int
	_ = a.DB.Raw().QueryRowContext(ctx, `
		select coalesce(last_scanned_message_id,0), coalesce(last_full_scan_at,''), coalesce(updated_at,'')
		from scan_state where channel_id=?`, channelID).Scan(&lastMsgID, &lastFull, &lastScan)
	out["last_scanned_message_id"] = lastMsgID
	out["last_full_scan_at"] = lastFull
	out["last_scan_at"] = lastScan
	var scanErrors int
	_ = a.DB.Raw().QueryRowContext(ctx, `select count(*) from scan_errors where channel_id=? and status='pending'`, channelID).Scan(&scanErrors)
	out["scan_errors_pending"] = scanErrors
	out["upload_limit_bytes"] = a.uploadLimit(ctx)
	return out, nil
}

// ScanOptions controls scan behavior.
type ScanOptions struct {
	Full   bool
	Strict bool
	Root   string
}

// Scan rebuilds or incrementally updates the index.
func (a *App) Scan(ctx context.Context, opts ScanOptions) (map[string]any, error) {
	channelID, tgID, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	tgChID, err := a.tgChannelID(ctx)
	if err != nil {
		return nil, err
	}
	var afterID int
	var mode string
	if opts.Full {
		mode = "full"
		_, _ = a.DB.Raw().ExecContext(ctx, `update files set status='missing' where channel_id=? and status='active'`, channelID)
	} else {
		mode = "incremental"
		_ = a.DB.Raw().QueryRowContext(ctx, `select coalesce(last_scanned_message_id,0) from scan_state where channel_id=?`, channelID).Scan(&afterID)
	}

	msgs, err := a.TG.History(ctx, tgChID, afterID, 0)
	if err != nil {
		return nil, mapTGErr(err)
	}
	manifestByMedia := map[int]struct {
		meta  manifest.ParsedMeta
		msgID int
	}{}
	for _, msg := range msgs {
		if msg.Text == "" || msg.ReplyTo == nil {
			continue
		}
		if meta, err := manifest.ParseManifestReply(msg.Text); err == nil {
			manifestByMedia[*msg.ReplyTo] = struct {
				meta  manifest.ParsedMeta
				msgID int
			}{meta: meta, msgID: msg.ID}
		}
	}
	invalid := 0
	now := time.Now().UTC().Format(time.RFC3339)
	seen := map[string]bool{}
	maxID := afterID
	scanActive, err := a.activePaths(ctx, channelID)
	if err != nil {
		return nil, err
	}
	scanSlugs, err := a.loadExistingSlugs(ctx, channelID)
	if err != nil {
		return nil, err
	}

	for _, msg := range msgs {
		if msg.ID > maxID {
			maxID = msg.ID
		}
		if msg.Caption == "" && msg.Text == "" {
			continue
		}
		var meta manifest.ParsedMeta
		var parseErr error
		mediaMessageID := msg.ID
		if msg.Caption != "" {
			meta, parseErr = manifest.ParseCaption(msg.Caption)
		}
		if parseErr != nil && msg.Text != "" {
			meta, parseErr = manifest.ParseManifestReply(msg.Text)
			if parseErr == nil && msg.Caption == "" && msg.ReplyTo != nil {
				continue
			}
		}
		if parseErr != nil {
			invalid++
			_, _ = a.DB.Raw().ExecContext(ctx, `
				insert into scan_errors(channel_id,message_id,error_code,error_message,raw_excerpt,status,first_seen_at,last_seen_at)
				values(?,?,?,?,?,'pending',?,?)
				on conflict(channel_id,message_id,error_code) do update set last_seen_at=excluded.last_seen_at`,
				channelID, msg.ID, apperr.ErrManifestInvalid, parseErr.Error(), truncate(msg.Caption+msg.Text, 200), now, now)
			if opts.Strict {
				return nil, apperr.New(apperr.ErrScanFailed, "invalid managed messages found")
			}
			continue
		}
		var manifestMsgID *int
		if meta.ManifestReply {
			resolved, ok := manifestByMedia[msg.ID]
			if !ok {
				invalid++
				_, _ = a.DB.Raw().ExecContext(ctx, `
					insert into scan_errors(channel_id,message_id,error_code,error_message,raw_excerpt,status,first_seen_at,last_seen_at)
					values(?,?,?,?,?,'pending',?,?)
					on conflict(channel_id,message_id,error_code) do update set last_seen_at=excluded.last_seen_at`,
					channelID, msg.ID, apperr.ErrManifestInvalid, "manifest reply not found", truncate(msg.Caption, 200), now, now)
				if opts.Strict {
					return nil, apperr.New(apperr.ErrScanFailed, "manifest reply missing")
				}
				continue
			}
			meta = resolved.meta
			id := resolved.msgID
			manifestMsgID = &id
		}
		if meta.CanonicalPath == "" {
			continue
		}
		if meta.Size == 0 && msg.FileSize > 0 {
			meta.Size = msg.FileSize
		}
		if meta.MIME == "" && msg.MIME != "" {
			meta.MIME = msg.MIME
		}
		if meta.DisplayName == "" && msg.FileName != "" {
			meta.DisplayName = msg.FileName
		}
		seen[meta.CanonicalPath] = true
		indexed, err := a.indexScannedFile(ctx, channelID, mediaMessageID, manifestMsgID, meta, now, scanActive, scanSlugs)
		if err != nil {
			return nil, err
		}
		if indexed {
			scanActive = appendScanIndexedPath(scanActive, meta.CanonicalPath)
			_, _ = a.DB.Raw().ExecContext(ctx, `update scan_errors set status='resolved', resolved_at=? where channel_id=? and message_id=?`, now, channelID, mediaMessageID)
		}
	}

	if opts.Full {
		rows, _ := a.DB.Raw().QueryContext(ctx, `select canonical_path from files where channel_id=? and status='active'`, channelID)
		if rows != nil {
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var cp string
				_ = rows.Scan(&cp)
				if !seen[cp] {
					_, _ = a.DB.Raw().ExecContext(ctx, `update files set status='missing', updated_at=? where channel_id=? and canonical_path=? and status='active'`, now, channelID, cp)
				}
			}
		}
	}
	_ = a.DB.RunDirectoryGC(ctx, channelID)

	fullAt := ""
	if opts.Full {
		fullAt = now
	}
	_, _ = a.DB.Raw().ExecContext(ctx, `
		insert into scan_state(channel_id,last_scanned_message_id,last_full_scan_at,updated_at) values(?,?,?,?)
		on conflict(channel_id) do update set last_scanned_message_id=excluded.last_scanned_message_id,
			last_full_scan_at=coalesce(excluded.last_full_scan_at, scan_state.last_full_scan_at), updated_at=excluded.updated_at`,
		channelID, maxID, nullIfEmpty(fullAt), now)

	counts := map[string]int{}
	rows, _ := a.DB.Raw().QueryContext(ctx, `select status, count(*) from files where channel_id=? group by status`, channelID)
	if rows != nil {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var s string
			var n int
			_ = rows.Scan(&s, &n)
			counts[s] = n
		}
	}
	return map[string]any{
		"mode":    mode,
		"channel": tgID,
		"active":  counts["active"],
		"deleted": counts["deleted"],
		"invalid": invalid + counts["invalid"],
		"missing": counts["missing"],
	}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// DownloadFile downloads a remote file to local path.
func (a *App) DownloadFile(ctx context.Context, remotePath, localDest string, policy ConflictPolicy) error {
	p, err := fsmodel.NormalizeCanonicalPath(remotePath)
	if err != nil {
		return err
	}
	channelID, _, err := a.channelID(ctx)
	if err != nil {
		return err
	}
	tgChID, err := a.tgChannelID(ctx)
	if err != nil {
		return err
	}
	var messageID int
	var size int64
	var hash string
	err = a.DB.Raw().QueryRowContext(ctx, `select message_id, size, content_hash from files where channel_id=? and canonical_path=? and status='active'`, channelID, p).Scan(&messageID, &size, &hash)
	if err == sql.ErrNoRows {
		return apperr.New(apperr.ErrRemoteNotFound, p)
	}
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "lookup file", err)
	}
	if _, err := a.files().Stat(ctx, localDest); err == nil {
		switch policy {
		case ConflictSkip:
			return nil
		case ConflictReplace:
		case ConflictRename:
			localDest = autoRenameLocal(ctx, a.files(), localDest)
		default:
			return apperr.New(apperr.ErrLocalPathExists, localDest)
		}
	}
	if err := a.files().MkdirAll(ctx, filepath.Dir(localDest), 0o755); err != nil {
		return err
	}
	tmp, f, err := a.files().CreateTemp(ctx, localDest)
	if err != nil {
		return err
	}
	hashEnabled := hash != "" && a.Cfg.Hash.Enabled && strings.HasPrefix(hash, "blake3:")
	var w io.Writer = f
	var h *blake3.Hasher
	if hashEnabled {
		h = blake3.New(32, nil)
		w = io.MultiWriter(f, h)
	}
	if err := a.TG.DownloadMedia(ctx, tgChID, messageID, w); err != nil {
		_ = f.Close()
		_ = a.files().Remove(ctx, tmp)
		return mapTGErr(err)
	}
	if err := f.Close(); err != nil {
		_ = a.files().Remove(ctx, tmp)
		return err
	}
	if size > 0 {
		info, err := a.files().Stat(ctx, tmp)
		if err != nil {
			_ = a.files().Remove(ctx, tmp)
			return err
		}
		if info.Size != size {
			_ = a.files().Remove(ctx, tmp)
			return apperr.New(apperr.ErrTelegramRPC, "size mismatch")
		}
	}
	if hashEnabled {
		got := "blake3:" + hex.EncodeToString(h.Sum(nil))
		if got != hash {
			_ = a.files().Remove(ctx, tmp)
			return apperr.New(apperr.ErrTelegramRPC, "content hash mismatch")
		}
	}
	if err := a.files().Rename(ctx, tmp, localDest); err != nil {
		_ = a.files().Remove(ctx, tmp)
		return err
	}
	return nil
}

func autoRenameLocal(ctx context.Context, files ports.FileSystem, path string) string {
	for i := 1; i < 1000; i++ {
		candidate := strings.TrimSuffix(path, filepath.Ext(path)) + fmt.Sprintf(" (%d)", i) + filepath.Ext(path)
		if _, err := files.Stat(ctx, candidate); err != nil {
			return candidate
		}
	}
	return path
}

// MoveFile moves or renames a remote file.
func (a *App) MoveFile(ctx context.Context, from, to string) error {
	src, err := fsmodel.NormalizeCanonicalPath(from)
	if err != nil {
		return err
	}
	channelID, _, err := a.channelID(ctx)
	if err != nil {
		return err
	}
	tgChID, err := a.tgChannelID(ctx)
	if err != nil {
		return err
	}
	active, err := a.activePaths(ctx, channelID)
	if err != nil {
		return err
	}
	if fsmodel.IsDirectorySource(src, active) {
		return apperr.New(apperr.ErrDirectoryMoveUnsupported, src)
	}
	dst, err := fsmodel.MoveDestination(src, to, active)
	if err != nil {
		return err
	}
	var fileID int64
	var messageID, manifestID sql.NullInt64
	var displayName, contentHash, mimeType string
	var size int64
	err = a.DB.Raw().QueryRowContext(ctx, `select id, message_id, manifest_message_id, display_name, size, content_hash, mime from files where channel_id=? and canonical_path=? and status='active'`,
		channelID, src).Scan(&fileID, &messageID, &manifestID, &displayName, &size, &contentHash, &mimeType)
	if err == sql.ErrNoRows {
		return apperr.New(apperr.ErrRemoteNotFound, src)
	}
	owner := newOwnerToken()
	ttl := time.Duration(a.Cfg.Locks.TTLSeconds) * time.Second
	for _, p := range []string{src, dst} {
		if err := a.DB.AcquireLock(ctx, sqlitestore.LockKey(channelID, p), owner, ttl); err != nil {
			return err
		}
	}
	defer func() {
		_ = a.DB.ReleaseLock(ctx, sqlitestore.LockKey(channelID, src), owner)
		_ = a.DB.ReleaseLock(ctx, sqlitestore.LockKey(channelID, dst), owner)
	}()

	existingSlugs, err := a.loadExistingSlugs(ctx, channelID)
	if err != nil {
		return err
	}
	tags, _, _ := pathcodec.GenerateChain(dst, existingSlugs)
	meta := manifest.FileMeta{
		CanonicalPath: dst,
		DisplayName:   fsmodel.BaseName(dst),
		ParentHuman:   fsmodel.HumanParent(dst),
		Size:          size,
		Hash:          contentHash,
		MIME:          mimeType,
		Tags:          tags,
	}
	capRes, err := manifest.RenderCaption(meta, a.Cfg.Caption.SafeMediaCaptionUTF16Units, a.Cfg.Caption.MarginUTF16Units)
	if err != nil {
		return err
	}
	if !messageID.Valid {
		return apperr.New(apperr.ErrRemoteNotFound, src)
	}
	if err := a.TG.EditCaption(ctx, tgChID, int(messageID.Int64), capRes.Caption); err != nil {
		return mapTGErr(err)
	}
	if capRes.NeedsManifestReply && manifestID.Valid {
		_ = a.TG.EditText(ctx, tgChID, int(manifestID.Int64), capRes.ManifestReply)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	err = a.DB.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update files set canonical_path=?, display_name=?, updated_at=? where id=?`,
			dst, fsmodel.BaseName(dst), now, fileID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `delete from path_tags where file_id=?`, fileID)
		if err != nil {
			return err
		}
		for i, tag := range tags {
			_, err = tx.ExecContext(ctx, `insert into path_tags(file_id,tag,depth) values(?,?,?)`, fileID, tag, i)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return a.DB.RunDirectoryGC(ctx, channelID)
}

// DeleteFile removes a remote file.
func (a *App) DeleteFile(ctx context.Context, remotePath string) error {
	p, err := fsmodel.NormalizeCanonicalPath(remotePath)
	if err != nil {
		return err
	}
	channelID, _, err := a.channelID(ctx)
	if err != nil {
		return err
	}
	tgChID, err := a.tgChannelID(ctx)
	if err != nil {
		return err
	}
	active, err := a.activePaths(ctx, channelID)
	if err != nil {
		return err
	}
	for _, ap := range active {
		if ap.IsDir && ap.Canonical == p {
			return apperr.New(apperr.ErrDirectoryDeleteUnsupported, p)
		}
	}
	var fileID int64
	var messageID, manifestID sql.NullInt64
	err = a.DB.Raw().QueryRowContext(ctx, `select id, message_id, manifest_message_id from files where channel_id=? and canonical_path=? and status='active'`, channelID, p).Scan(&fileID, &messageID, &manifestID)
	if err == sql.ErrNoRows {
		return apperr.New(apperr.ErrRemoteNotFound, p)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if a.Cfg.Delete.Mode == "delete" {
		if messageID.Valid {
			if err := a.TG.DeleteMessage(ctx, tgChID, int(messageID.Int64)); err != nil {
				return mapTGErr(err)
			}
		}
		if manifestID.Valid {
			_ = a.TG.DeleteMessage(ctx, tgChID, int(manifestID.Int64))
		}
	} else {
		if messageID.Valid {
			if err := a.TG.EditCaption(ctx, tgChID, int(messageID.Int64), fsmodel.BaseName(p)+"\n\ntd:v1 deleted=true"); err != nil {
				return mapTGErr(err)
			}
		}
		if manifestID.Valid {
			_ = a.TG.EditText(ctx, tgChID, int(manifestID.Int64), "td-manifest:v1\ndeleted=true")
		}
	}
	err = a.DB.WithTx(ctx, func(tx *sql.Tx) error {
		_ = a.DB.ClearNodeID(ctx, tx, fileID)
		_, err := tx.ExecContext(ctx, `update files set status='deleted', updated_at=? where id=?`, now, fileID)
		return err
	})
	if err != nil {
		return err
	}
	return a.DB.RunDirectoryGC(ctx, channelID)
}

// RepairPending repairs pending uploads.
func (a *App) RepairPending(ctx context.Context) (map[string]any, error) {
	channelID, _, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := a.DB.Raw().QueryContext(ctx, `select id, canonical_path, original_local_path, message_id from files where channel_id=? and status in ('pending','orphaned')`, channelID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	repaired, invalid := 0, 0
	for rows.Next() {
		var id int64
		var path, local sql.NullString
		var msgID sql.NullInt64
		_ = rows.Scan(&id, &path, &local, &msgID)
		if local.Valid && path.Valid {
			if _, err := a.UploadFile(ctx, local.String, path.String, ConflictReplace, false); err == nil {
				repaired++
				continue
			}
		}
		_, _ = a.DB.Raw().ExecContext(ctx, `update files set status='invalid' where id=?`, id)
		invalid++
	}
	return map[string]any{"repaired": repaired, "invalid": invalid}, nil
}

// RepairOrphaned indexes orphaned Telegram messages.
func (a *App) RepairOrphaned(ctx context.Context) (map[string]any, error) {
	res, err := a.Scan(ctx, ScanOptions{Full: false})
	if err != nil {
		return nil, err
	}
	return map[string]any{"indexed": res["active"]}, nil
}

// RepairScanErrors retries scan error resolution.
func (a *App) RepairScanErrors(ctx context.Context) (map[string]any, error) {
	res, err := a.Scan(ctx, ScanOptions{Full: false})
	if err != nil {
		return nil, err
	}
	return map[string]any{"resolved": res["invalid"]}, nil
}

// UploadRecursive uploads a directory recursively.
func (a *App) UploadRecursive(ctx context.Context, localDir, remoteDir string, policy ConflictPolicy, continueOnError bool, noHash bool) (map[string]any, error) {
	remoteDir, err := fsmodel.NormalizeCanonicalPath(remoteDir)
	if err != nil {
		return nil, err
	}
	var files []string
	err = filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	uploaded, failed := 0, 0
	var errs []string
	for _, f := range files {
		rel, _ := filepath.Rel(localDir, f)
		dest := remoteDir
		if dest != "/" {
			dest += "/"
		}
		dest += filepath.ToSlash(rel)
		_, err := a.UploadFile(ctx, f, dest, policy, noHash)
		if err != nil {
			failed++
			errs = append(errs, err.Error())
			if !continueOnError {
				return nil, err
			}
			continue
		}
		uploaded++
	}
	return map[string]any{"uploaded": uploaded, "failed": failed, "errors": errs}, nil
}

// DownloadRecursive downloads a directory tree.
func (a *App) DownloadRecursive(ctx context.Context, remotePath, localDir string, policy ConflictPolicy, continueOnError bool) error {
	entries, err := a.ListDir(ctx, remotePath)
	if err != nil {
		return err
	}
	for _, e := range entries {
		localPath := filepath.Join(localDir, e.Name)
		if e.Type == "dir" {
			if err := os.MkdirAll(localPath, 0o755); err != nil {
				if !continueOnError {
					return err
				}
				continue
			}
			if err := a.DownloadRecursive(ctx, e.Path, localPath, policy, continueOnError); err != nil && !continueOnError {
				return err
			}
			continue
		}
		if err := a.DownloadFile(ctx, e.Path, localPath, policy); err != nil && !continueOnError {
			return err
		}
	}
	return nil
}

func appendScanIndexedPath(active []fsmodel.ActivePath, canonical string) []fsmodel.ActivePath {
	out := make([]fsmodel.ActivePath, 0, len(active)+4)
	for _, ap := range active {
		if ap.Canonical != canonical {
			out = append(out, ap)
		}
	}
	out = append(out, fsmodel.ActivePath{Canonical: canonical, IsDir: false})
	for anc := range fsmodel.DeriveDirectoryNodes([]string{canonical}) {
		found := false
		for _, ap := range out {
			if ap.Canonical == anc && ap.IsDir {
				found = true
				break
			}
		}
		if !found {
			out = append(out, fsmodel.ActivePath{Canonical: anc, IsDir: true})
		}
	}
	return out
}

func (a *App) indexScannedFile(ctx context.Context, channelID int64, messageID int, manifestMsgID *int, meta manifest.ParsedMeta, now string, active []fsmodel.ActivePath, slugMap map[string]string) (bool, error) {
	// Exclude the path being re-indexed from conflict checks.
	filtered := make([]fsmodel.ActivePath, 0, len(active))
	for _, ap := range active {
		if ap.Canonical != meta.CanonicalPath {
			filtered = append(filtered, ap)
		}
	}
	if conflictErr := fsmodel.CheckUploadConflict(meta.CanonicalPath, filtered); conflictErr != nil {
		code := apperr.ErrPathInvalid
		if ae, ok := apperr.As(conflictErr); ok {
			code = ae.Code
		}
		_, _ = a.DB.Raw().ExecContext(ctx, `
			insert into scan_errors(channel_id,message_id,error_code,error_message,raw_excerpt,status,first_seen_at,last_seen_at)
			values(?,?,?,?,?,'pending',?,?)
			on conflict(channel_id,message_id,error_code) do update set last_seen_at=excluded.last_seen_at`,
			channelID, messageID, code, conflictErr.Error(), truncate(meta.CanonicalPath, 200), now, now)
		return false, nil
	}

	var mfID any
	if manifestMsgID != nil {
		mfID = *manifestMsgID
	}
	var fileID int64
	err := a.DB.Raw().QueryRowContext(ctx, `
		select id from files where channel_id=? and canonical_path=? and status not in ('superseded','deleted')`,
		channelID, meta.CanonicalPath).Scan(&fileID)
	if err == sql.ErrNoRows {
		err = a.DB.Raw().QueryRowContext(ctx, `
			select id from files where channel_id=? and message_id=? and status not in ('superseded','deleted')`,
			channelID, messageID).Scan(&fileID)
	}
	switch err {
	case nil:
		_, err = a.DB.Raw().ExecContext(ctx, `
			update files set message_id=?, manifest_message_id=?, canonical_path=?, display_name=?, size=?, content_hash=?, mime=?, status='active', updated_at=?
			where id=?`,
			messageID, mfID, meta.CanonicalPath, meta.DisplayName, meta.Size, meta.Hash, meta.MIME, now, fileID)
	case sql.ErrNoRows:
		_, err = a.DB.Raw().ExecContext(ctx, `
			insert into files(channel_id,message_id,manifest_message_id,canonical_path,display_name,size,content_hash,mime,status,uploaded_at,updated_at)
			values(?,?,?,?,?,?,?,?,'active',?,?)`,
			channelID, messageID, mfID, meta.CanonicalPath, meta.DisplayName, meta.Size, meta.Hash, meta.MIME, now, now)
	default:
		return false, apperr.Wrap(apperr.ErrDB, "lookup scanned file", err)
	}
	if err != nil {
		return false, apperr.Wrap(apperr.ErrDB, "index scanned file", err)
	}
	if fileID == 0 {
		_ = a.DB.Raw().QueryRowContext(ctx, `select id from files where channel_id=? and canonical_path=? and status='active'`,
			channelID, meta.CanonicalPath).Scan(&fileID)
	}
	tags, slugMaps, _ := pathcodec.GenerateChain(meta.CanonicalPath, slugMap)
	for _, sm := range slugMaps {
		_, _ = a.DB.Raw().ExecContext(ctx, `insert or ignore into path_segment_slugs(channel_id,parent_canonical_path,segment,slug,hash_len,created_at) values(?,?,?,?,?,?)`,
			channelID, sm.ParentCanonical, sm.Segment, sm.Slug, sm.HashLen, now)
		slugMap[sm.ParentCanonical+"|"+sm.Segment] = sm.Slug
	}
	_ = a.DB.Raw().QueryRowContext(ctx, `select id from files where channel_id=? and canonical_path=? and status='active'`, channelID, meta.CanonicalPath).Scan(&fileID)
	for i, tag := range tags {
		_, _ = a.DB.Raw().ExecContext(ctx, `insert or ignore into path_tags(file_id,tag,depth) values(?,?,?)`, fileID, tag, i)
	}
	for anc, name := range fsmodel.DeriveDirectoryNodes([]string{meta.CanonicalPath}) {
		_, _ = a.DB.Raw().ExecContext(ctx, `insert or ignore into nodes(channel_id,canonical_path,parent_path,display_name,type,derived,created_at,updated_at) values(?,?,?,?,'dir',1,?,?)`,
			channelID, anc, fsmodel.ParentPath(anc), name, now, now)
	}
	return true, nil
}
