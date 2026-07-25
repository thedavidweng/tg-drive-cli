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

	"github.com/thedavidweng/tg-drive-cli/internal/apperr"
	"github.com/thedavidweng/tg-drive-cli/internal/db"
	"github.com/thedavidweng/tg-drive-cli/internal/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/internal/manifest"
	"github.com/thedavidweng/tg-drive-cli/internal/pathcodec"
	"github.com/thedavidweng/tg-drive-cli/internal/telegram"
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
		order by type desc, display_name`, channelID, escapeLike(prefix)+"%", channelID, p)
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

// escapeLike escapes SQLite LIKE wildcards so path segments containing
// '_' or '%' match literally (queries use ESCAPE '\').
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
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
	nowT := time.Now().UTC()
	staleCutoff := nowT.Add(-time.Duration(a.Cfg.Locks.TTLSeconds) * time.Second).Format(time.RFC3339)
	var stalePending int
	_ = a.DB.Raw().QueryRowContext(ctx, `select count(*) from files where channel_id=? and status='pending' and updated_at < ?`, channelID, staleCutoff).Scan(&stalePending)
	out["stale_pending"] = stalePending
	var staleLocks int
	_ = a.DB.Raw().QueryRowContext(ctx, `select count(*) from operation_locks where expires_at < ?`, nowT.Format(time.RFC3339)).Scan(&staleLocks)
	out["stale_locks"] = staleLocks
	out["orphaned"] = counts["orphaned"]
	out["upload_limit_bytes"] = a.uploadLimit(ctx)
	return out, nil
}

// ScanOptions controls scan behavior.
type ScanOptions struct {
	Full           bool
	Strict         bool
	Repair         bool
	IncludeDeleted bool
	Root           string
}

// Scan rebuilds or incrementally updates the index. Existing index state is
// never modified before Telegram history has been fetched successfully, so a
// failed or aborted scan cannot corrupt the local cache.
func (a *App) Scan(ctx context.Context, opts ScanOptions) (map[string]any, error) {
	channelID, tgID, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	tgChID, err := a.tgChannelID(ctx)
	if err != nil {
		return nil, err
	}
	root := "/"
	if opts.Root != "" {
		root, err = fsmodel.NormalizeCanonicalPath(opts.Root)
		if err != nil {
			return nil, err
		}
	}
	inRoot := func(p string) bool {
		return root == "/" || p == root || strings.HasPrefix(p, root+"/")
	}
	var afterID int
	mode := "full"
	if !opts.Full {
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
	tombstones := 0
	strictFailure := ""
	now := time.Now().UTC().Format(time.RFC3339)
	seen := map[string]bool{}
	seenMsgIDs := map[int]bool{}
	maxID := afterID

	for _, msg := range msgs {
		if msg.ID > maxID {
			maxID = msg.ID
		}
		seenMsgIDs[msg.ID] = true
		if msg.Caption == "" && msg.Text == "" {
			continue
		}
		var meta manifest.ParsedMeta
		var parseErr error
		mediaMessageID := msg.ID
		if msg.Caption != "" {
			meta, parseErr = manifest.ParseCaption(msg.Caption)
		} else {
			// Text-only messages are manifest replies (already resolved via
			// manifestByMedia) or unmanaged messages; skip both.
			continue
		}
		if parseErr != nil {
			invalid++
			_, _ = a.DB.Raw().ExecContext(ctx, `
				insert into scan_errors(channel_id,message_id,error_code,error_message,raw_excerpt,status,first_seen_at,last_seen_at)
				values(?,?,?,?,?,'pending',?,?)
				on conflict(channel_id,message_id,error_code) do update set last_seen_at=excluded.last_seen_at, status='pending'`,
				channelID, msg.ID, apperr.ErrManifestInvalid, parseErr.Error(), truncate(msg.Caption+msg.Text, 200), now, now)
			if opts.Strict && strictFailure == "" {
				strictFailure = "invalid managed messages found"
			}
			continue
		}
		if meta.Deleted {
			if opts.IncludeDeleted && meta.CanonicalPath != "" && inRoot(meta.CanonicalPath) {
				tombstones++
				_, _ = a.DB.Raw().ExecContext(ctx, `update files set status='deleted', node_id=null, updated_at=? where channel_id=? and message_id=? and status in ('active','missing')`,
					now, channelID, mediaMessageID)
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
					on conflict(channel_id,message_id,error_code) do update set last_seen_at=excluded.last_seen_at, status='pending'`,
					channelID, msg.ID, apperr.ErrManifestInvalid, "manifest reply not found", truncate(msg.Caption, 200), now, now)
				if opts.Strict && strictFailure == "" {
					strictFailure = "manifest reply missing"
				}
				continue
			}
			if resolved.meta.Deleted {
				if opts.IncludeDeleted {
					tombstones++
					_, _ = a.DB.Raw().ExecContext(ctx, `update files set status='deleted', node_id=null, updated_at=? where channel_id=? and message_id=? and status in ('active','missing')`,
						now, channelID, mediaMessageID)
				}
				continue
			}
			meta = resolved.meta
			id := resolved.msgID
			manifestMsgID = &id
		}
		if meta.CanonicalPath == "" || !inRoot(meta.CanonicalPath) {
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
		if err := a.indexScannedFile(ctx, channelID, mediaMessageID, manifestMsgID, meta, now); err != nil {
			return nil, err
		}
		_, _ = a.DB.Raw().ExecContext(ctx, `update scan_errors set status='resolved', resolved_at=? where channel_id=? and message_id=? and status='pending'`, now, channelID, mediaMessageID)
	}

	if opts.Full {
		err = a.DB.WithTx(ctx, func(tx *sql.Tx) error {
			// Mark previously active files absent from this full pass missing.
			rows, err := tx.QueryContext(ctx, `select id, canonical_path from files where channel_id=? and status='active'`, channelID)
			if err != nil {
				return err
			}
			type row struct {
				id int64
				cp string
			}
			var stale []row
			for rows.Next() {
				var r row
				if err := rows.Scan(&r.id, &r.cp); err != nil {
					_ = rows.Close()
					return err
				}
				if inRoot(r.cp) && !seen[r.cp] {
					stale = append(stale, r)
				}
			}
			_ = rows.Close()
			for _, r := range stale {
				if _, err := tx.ExecContext(ctx, `update files set status='missing', node_id=null, updated_at=? where id=?`, now, r.id); err != nil {
					return err
				}
			}
			// Resolve scan errors whose message disappeared from the channel.
			errRows, err := tx.QueryContext(ctx, `select id, message_id from scan_errors where channel_id=? and status='pending'`, channelID)
			if err != nil {
				return err
			}
			var gone []int64
			for errRows.Next() {
				var id int64
				var msgID sql.NullInt64
				if err := errRows.Scan(&id, &msgID); err != nil {
					_ = errRows.Close()
					return err
				}
				if msgID.Valid && !seenMsgIDs[int(msgID.Int64)] {
					gone = append(gone, id)
				}
			}
			_ = errRows.Close()
			for _, id := range gone {
				if _, err := tx.ExecContext(ctx, `update scan_errors set status='resolved', resolved_at=? where id=?`, now, id); err != nil {
					return err
				}
			}
			return a.rebuildNodesTx(ctx, tx, channelID, now)
		})
		if err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "finalize full scan", err)
		}
	} else {
		_ = a.DB.RunDirectoryGC(ctx, channelID)
	}
	if opts.Repair {
		if err := a.DB.WithTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `delete from scan_errors where channel_id=? and status='resolved'`, channelID); err != nil {
				return err
			}
			return a.rebuildNodesTx(ctx, tx, channelID, now)
		}); err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "repair", err)
		}
	}

	fullAt := ""
	if opts.Full {
		fullAt = now
	}
	_, _ = a.DB.Raw().ExecContext(ctx, `
		insert into scan_state(channel_id,last_scanned_message_id,last_full_scan_at,updated_at) values(?,?,?,?)
		on conflict(channel_id) do update set last_scanned_message_id=excluded.last_scanned_message_id,
			last_full_scan_at=coalesce(excluded.last_full_scan_at, scan_state.last_full_scan_at), updated_at=excluded.updated_at`,
		channelID, maxID, nullIfEmpty(fullAt), now)

	if strictFailure != "" {
		return nil, apperr.New(apperr.ErrScanFailed, strictFailure)
	}

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
	out := map[string]any{
		"mode":    mode,
		"channel": tgID,
		"active":  counts["active"],
		"deleted": counts["deleted"],
		"invalid": invalid + counts["invalid"],
		"missing": counts["missing"],
	}
	if opts.IncludeDeleted {
		out["tombstones"] = tombstones
	}
	if !opts.Full {
		var lastFull string
		_ = a.DB.Raw().QueryRowContext(ctx, `select coalesce(last_full_scan_at,'') from scan_state where channel_id=?`, channelID).Scan(&lastFull)
		warn := ""
		if lastFull == "" {
			warn = "no full scan recorded; incremental scans cannot detect old-message edits or deletions — run td scan --full"
		} else if t, err := time.Parse(time.RFC3339, lastFull); err == nil && time.Since(t) > 7*24*time.Hour {
			warn = "last full scan was " + lastFull + "; run td scan --full to detect old-message drift"
		}
		if warn != "" {
			out["full_scan_warning"] = warn
		}
	}
	return out, nil
}

// rebuildNodesTx truncates derived directory nodes and rebuilds them from the
// current active file set.
func (a *App) rebuildNodesTx(ctx context.Context, tx *sql.Tx, channelID int64, now string) error {
	if _, err := tx.ExecContext(ctx, `delete from nodes where channel_id=? and derived=1`, channelID); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `select canonical_path from files where channel_id=? and status='active'`, channelID)
	if err != nil {
		return err
	}
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			_ = rows.Close()
			return err
		}
		paths = append(paths, p)
	}
	_ = rows.Close()
	for anc, name := range fsmodel.DeriveDirectoryNodes(paths) {
		if _, err := tx.ExecContext(ctx, `insert or ignore into nodes(channel_id,canonical_path,parent_path,display_name,type,derived,created_at,updated_at) values(?,?,?,?,'dir',1,?,?)`,
			channelID, anc, fsmodel.ParentPath(anc), name, now, now); err != nil {
			return err
		}
	}
	return nil
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

// DownloadFile downloads a remote file to local path, streaming through a
// temp file and verifying size/hash before the atomic rename.
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
	if _, err := os.Stat(localDest); err == nil {
		switch policy {
		case ConflictSkip:
			return nil
		case ConflictReplace:
		case ConflictRename:
			localDest = autoRenameLocal(localDest)
		default:
			return apperr.New(apperr.ErrLocalPathExists, localDest)
		}
	}
	if err := os.MkdirAll(filepath.Dir(localDest), 0o755); err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(localDest), ".td-download-*")
	if err != nil {
		return err
	}
	tmp := tmpFile.Name()
	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
	}
	hasher := blake3.New(32, nil)
	var written int64
	w := io.MultiWriter(tmpFile, hasher, countWriter{&written})
	if err := a.TG.DownloadMediaTo(ctx, tgChID, messageID, w); err != nil {
		cleanup()
		return mapTGErr(err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if size > 0 && written != size {
		_ = os.Remove(tmp)
		return apperr.New(apperr.ErrTelegramRPC, fmt.Sprintf("size mismatch: got %d want %d", written, size))
	}
	if hash != "" && a.Cfg.Hash.Enabled && strings.HasPrefix(hash, "blake3:") {
		got := "blake3:" + hex.EncodeToString(hasher.Sum(nil))
		if got != hash {
			_ = os.Remove(tmp)
			return apperr.New(apperr.ErrTelegramRPC, "content hash mismatch")
		}
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, localDest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

type countWriter struct{ n *int64 }

func (c countWriter) Write(p []byte) (int, error) {
	*c.n += int64(len(p))
	return len(p), nil
}

func autoRenameLocal(path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	for i := 1; i < 1000; i++ {
		candidate := filepath.Join(dir, fsmodel.ConflictRenameCandidate(base, i))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return path
}

// MoveFile moves or renames a remote file within the configured channel.
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
	if dst == src {
		return nil
	}
	// Validate the destination before any Telegram mutation.
	remaining := make([]fsmodel.ActivePath, 0, len(active))
	for _, ap := range active {
		if !ap.IsDir && ap.Canonical == src {
			continue
		}
		remaining = append(remaining, ap)
	}
	if err := fsmodel.CheckUploadConflict(dst, remaining); err != nil {
		return err
	}
	var fileID int64
	var messageID, manifestID sql.NullInt64
	var displayName, contentHash, mimeType string
	var size int64
	err = a.DB.Raw().QueryRowContext(ctx, `select id, message_id, manifest_message_id, display_name, size, content_hash, mime from files where channel_id=? and canonical_path=? and status='active'`,
		channelID, src).Scan(&fileID, &messageID, &manifestID, &displayName, &size, &contentHash, &mimeType)
	if err == sql.ErrNoRows {
		var otherChannel int64
		if scanErr := a.DB.Raw().QueryRowContext(ctx, `select channel_id from files where canonical_path=? and status='active' and channel_id != ? limit 1`,
			src, channelID).Scan(&otherChannel); scanErr == nil {
			return apperr.New(apperr.ErrCrossChannelMove, "source file belongs to a different channel; V1 supports same-channel moves only")
		}
		return apperr.New(apperr.ErrRemoteNotFound, src)
	}
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "lookup source", err)
	}
	owner := newOwnerToken()
	ttl := time.Duration(a.Cfg.Locks.TTLSeconds) * time.Second
	lockKeys := []string{db.LockKey(channelID, src), db.LockKey(channelID, dst)}
	sort.Strings(lockKeys)
	var held []string
	defer func() {
		for _, k := range held {
			_ = a.DB.ReleaseLock(ctx, k, owner)
		}
	}()
	for _, k := range lockKeys {
		if err := a.DB.AcquireLock(ctx, k, owner, ttl); err != nil {
			return err
		}
		held = append(held, k)
	}

	tags, moveSlugMaps, err := pathcodec.GenerateChain(dst, a.loadSlugMap(ctx, channelID))
	if err != nil {
		return err
	}
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
	// Manifest handling before the caption edit, so a failure here leaves the
	// message fully consistent with the old path.
	newManifestID := manifestID
	switch {
	case capRes.NeedsManifestReply && manifestID.Valid:
		if err := a.TG.EditText(ctx, tgChID, int(manifestID.Int64), capRes.ManifestReply); err != nil {
			return mapTGErr(err)
		}
	case capRes.NeedsManifestReply && !manifestID.Valid:
		id, err := a.TG.SendTextReply(ctx, tgChID, int(messageID.Int64), capRes.ManifestReply)
		if err != nil {
			return mapTGErr(err)
		}
		newManifestID = sql.NullInt64{Int64: int64(id), Valid: true}
	case !capRes.NeedsManifestReply && manifestID.Valid:
		// New caption is self-contained; refresh the reply so it never leaks
		// the old path. Best effort — the caption is authoritative.
		fullMeta := meta
		fullMeta.Tags = capRes.IncludedTags
		_ = a.TG.EditText(ctx, tgChID, int(manifestID.Int64), manifest.RenderManifestReply(fullMeta))
	}
	if err := a.TG.EditCaption(ctx, tgChID, int(messageID.Int64), capRes.Caption); err != nil {
		// Undo the manifest change so the message stays consistent with the
		// old path; otherwise a later scan would silently complete the move.
		switch {
		case capRes.NeedsManifestReply && !manifestID.Valid && newManifestID.Valid:
			_ = a.TG.DeleteMessage(ctx, tgChID, int(newManifestID.Int64))
		case capRes.NeedsManifestReply && manifestID.Valid:
			if oldTags, _, tagErr := pathcodec.GenerateChain(src, a.loadSlugMap(ctx, channelID)); tagErr == nil {
				oldMeta := manifest.FileMeta{
					CanonicalPath: src,
					DisplayName:   displayName,
					ParentHuman:   fsmodel.HumanParent(src),
					Size:          size,
					Hash:          contentHash,
					MIME:          mimeType,
					Tags:          oldTags,
				}
				_ = a.TG.EditText(ctx, tgChID, int(manifestID.Int64), manifest.RenderManifestReply(oldMeta))
			}
		}
		return mapTGErr(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	err = a.DB.WithTx(ctx, func(tx *sql.Tx) error {
		var mfID any
		if newManifestID.Valid {
			mfID = newManifestID.Int64
		}
		_, err := tx.ExecContext(ctx, `update files set canonical_path=?, display_name=?, manifest_message_id=?, node_id=null, updated_at=? where id=?`,
			dst, fsmodel.BaseName(dst), mfID, now, fileID)
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
		for _, sm := range moveSlugMaps {
			_, err = tx.ExecContext(ctx, `insert or ignore into path_segment_slugs(channel_id,parent_canonical_path,segment,slug,hash_len,created_at) values(?,?,?,?,?,?)`,
				channelID, sm.ParentCanonical, sm.Segment, sm.Slug, sm.HashLen, now)
			if err != nil {
				return err
			}
		}
		for anc, name := range fsmodel.DeriveDirectoryNodes([]string{dst}) {
			_, err = tx.ExecContext(ctx, `insert or ignore into nodes(channel_id,canonical_path,parent_path,display_name,type,derived,created_at,updated_at) values(?,?,?,?,'dir',1,?,?)`,
				channelID, anc, fsmodel.ParentPath(anc), name, now, now)
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

// DeleteOptions controls td rm behavior.
type DeleteOptions struct {
	// Tombstone forces tombstone mode regardless of configured delete.mode.
	Tombstone bool
	// AllowStaleManifest downgrades a failed manifest redaction/removal to a
	// warning instead of an error.
	AllowStaleManifest bool
}

// DeleteFile removes a remote file according to the delete policy.
func (a *App) DeleteFile(ctx context.Context, remotePath string, opts DeleteOptions) (map[string]any, error) {
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
	active, err := a.activePaths(ctx, channelID)
	if err != nil {
		return nil, err
	}
	for _, ap := range active {
		if ap.IsDir && ap.Canonical == p {
			return nil, apperr.New(apperr.ErrDirectoryDeleteUnsupported, p)
		}
	}
	var fileID int64
	var messageID, manifestID sql.NullInt64
	err = a.DB.Raw().QueryRowContext(ctx, `select id, message_id, manifest_message_id from files where channel_id=? and canonical_path=? and status='active'`, channelID, p).Scan(&fileID, &messageID, &manifestID)
	if err == sql.ErrNoRows {
		return nil, apperr.New(apperr.ErrRemoteNotFound, p)
	}
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "lookup file", err)
	}
	owner := newOwnerToken()
	lockKey := db.LockKey(channelID, p)
	ttl := time.Duration(a.Cfg.Locks.TTLSeconds) * time.Second
	if err := a.DB.AcquireLock(ctx, lockKey, owner, ttl); err != nil {
		return nil, err
	}
	defer func() { _ = a.DB.ReleaseLock(ctx, lockKey, owner) }()

	mode := a.Cfg.Delete.Mode
	if opts.Tombstone {
		mode = "tombstone"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var manifestErr error
	if mode == "delete" {
		if messageID.Valid {
			if err := a.TG.DeleteMessage(ctx, tgChID, int(messageID.Int64)); err != nil {
				return nil, mapTGErr(err)
			}
		}
		if manifestID.Valid {
			manifestErr = a.TG.DeleteMessage(ctx, tgChID, int(manifestID.Int64))
		}
	} else {
		if messageID.Valid {
			if err := a.TG.EditCaption(ctx, tgChID, int(messageID.Int64), manifest.RenderTombstoneCaption(fsmodel.BaseName(p), p)); err != nil {
				return nil, mapTGErr(err)
			}
		}
		if manifestID.Valid {
			manifestErr = a.TG.EditText(ctx, tgChID, int(manifestID.Int64), manifest.RenderTombstoneManifest(p))
		}
	}
	if manifestErr != nil {
		if _, notFound := manifestErr.(*telegram.MessageNotFoundError); notFound {
			manifestErr = nil
		} else if !opts.AllowStaleManifest {
			return nil, apperr.New(apperr.ErrTelegramRPC,
				fmt.Sprintf("manifest reply %d could not be redacted: %v; rerun with --allow-stale-manifest to ignore", manifestID.Int64, manifestErr))
		}
	}
	err = a.DB.WithTx(ctx, func(tx *sql.Tx) error {
		if err := a.DB.ClearNodeID(ctx, tx, fileID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `update files set status='deleted', updated_at=? where id=?`, now, fileID)
		return err
	})
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "mark deleted", err)
	}
	if err := a.DB.RunDirectoryGC(ctx, channelID); err != nil {
		return nil, err
	}
	out := map[string]any{"path": p, "mode": mode}
	if manifestErr != nil {
		out["stale_manifest"] = true
	}
	return out, nil
}

// RepairPending resolves stale pending rows and expired operation locks.
func (a *App) RepairPending(ctx context.Context) (map[string]any, error) {
	channelID, _, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	locksCleared := int64(0)
	if res, err := a.DB.Raw().ExecContext(ctx, `delete from operation_locks where expires_at < ?`, now); err == nil {
		locksCleared, _ = res.RowsAffected()
	}
	rows, err := a.DB.Raw().QueryContext(ctx, `select id, canonical_path, original_local_path, message_id from files where channel_id=? and status='pending'`, channelID)
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
	_ = rows.Close()
	repaired, invalid, orphaned := 0, 0, 0
	for _, r := range pending {
		switch {
		case r.msgID.Valid:
			// Upload reached Telegram but was never promoted; hand off to
			// orphan repair which can complete or delete it.
			_, _ = a.DB.Raw().ExecContext(ctx, `update files set status='orphaned', updated_at=? where id=?`, now, r.id)
			orphaned++
		case r.local.Valid && r.local.String != "":
			if _, statErr := os.Stat(r.local.String); statErr == nil {
				_, _ = a.DB.Raw().ExecContext(ctx, `delete from files where id=?`, r.id)
				if _, err := a.UploadFile(ctx, r.local.String, r.path, ConflictSkip, false); err == nil {
					repaired++
					continue
				}
				invalid++
				continue
			}
			_, _ = a.DB.Raw().ExecContext(ctx, `update files set status='invalid', updated_at=? where id=?`, now, r.id)
			invalid++
		default:
			_, _ = a.DB.Raw().ExecContext(ctx, `update files set status='invalid', updated_at=? where id=?`, now, r.id)
			invalid++
		}
	}
	return map[string]any{"repaired": repaired, "invalid": invalid, "orphaned": orphaned, "locks_cleared": locksCleared}, nil
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
	_ = rows.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	repaired, deleted, invalid := 0, 0, 0
	for _, r := range orphans {
		if !r.msgID.Valid {
			_, _ = a.DB.Raw().ExecContext(ctx, `update files set status='invalid', updated_at=? where id=?`, now, r.id)
			invalid++
			continue
		}
		if deleteOrphans {
			err := a.TG.DeleteMessage(ctx, tgChID, int(r.msgID.Int64))
			if _, notFound := err.(*telegram.MessageNotFoundError); err == nil || notFound {
				_, _ = a.DB.Raw().ExecContext(ctx, `update files set status='deleted', node_id=null, updated_at=? where id=?`, now, r.id)
				deleted++
				continue
			}
			invalid++
			continue
		}
		// Complete the interrupted upload: regenerate metadata and resend the
		// manifest reply, then promote the row.
		tags, slugMaps, err := pathcodec.GenerateChain(r.path, a.loadSlugMap(ctx, channelID))
		if err != nil {
			invalid++
			continue
		}
		meta := manifest.FileMeta{
			CanonicalPath: r.path,
			DisplayName:   r.name,
			ParentHuman:   fsmodel.HumanParent(r.path),
			Size:          r.size,
			Hash:          r.hash,
			MIME:          r.mimeT,
			Created:       now,
			Tags:          tags,
		}
		capRes, err := manifest.RenderCaption(meta, a.Cfg.Caption.SafeMediaCaptionUTF16Units, a.Cfg.Caption.MarginUTF16Units)
		if err != nil {
			invalid++
			continue
		}
		var manifestMsgID sql.NullInt64
		if capRes.NeedsManifestReply {
			id, err := a.TG.SendTextReply(ctx, tgChID, int(r.msgID.Int64), capRes.ManifestReply)
			if err != nil {
				continue // stays orphaned for a later attempt
			}
			manifestMsgID = sql.NullInt64{Int64: int64(id), Valid: true}
		}
		err = a.DB.WithTx(ctx, func(tx *sql.Tx) error {
			var mfID any
			if manifestMsgID.Valid {
				mfID = manifestMsgID.Int64
			}
			if _, err := tx.ExecContext(ctx, `update files set status='active', manifest_message_id=?, uploaded_at=?, updated_at=? where id=?`, mfID, now, now, r.id); err != nil {
				return err
			}
			for _, sm := range slugMaps {
				if _, err := tx.ExecContext(ctx, `insert or ignore into path_segment_slugs(channel_id,parent_canonical_path,segment,slug,hash_len,created_at) values(?,?,?,?,?,?)`,
					channelID, sm.ParentCanonical, sm.Segment, sm.Slug, sm.HashLen, now); err != nil {
					return err
				}
			}
			for i, tag := range capRes.IncludedTags {
				if _, err := tx.ExecContext(ctx, `insert or ignore into path_tags(file_id,tag,depth) values(?,?,?)`, r.id, tag, i); err != nil {
					return err
				}
			}
			for anc, name := range fsmodel.DeriveDirectoryNodes([]string{r.path}) {
				if _, err := tx.ExecContext(ctx, `insert or ignore into nodes(channel_id,canonical_path,parent_path,display_name,type,derived,created_at,updated_at) values(?,?,?,?,'dir',1,?,?)`,
					channelID, anc, fsmodel.ParentPath(anc), name, now, now); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			invalid++
			continue
		}
		repaired++
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
	var displayName, contentHash, mimeType string
	var size int64
	err = a.DB.Raw().QueryRowContext(ctx, `select id, message_id, manifest_message_id, display_name, coalesce(size,0), coalesce(content_hash,''), coalesce(mime,'') from files where channel_id=? and canonical_path=? and status='active'`,
		channelID, p).Scan(&fileID, &messageID, &manifestID, &displayName, &size, &contentHash, &mimeType)
	if err == sql.ErrNoRows {
		return nil, apperr.New(apperr.ErrRemoteNotFound, p)
	}
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "lookup file", err)
	}
	if !messageID.Valid {
		return nil, apperr.New(apperr.ErrRemoteNotFound, p)
	}
	tags, slugMaps, err := pathcodec.GenerateChain(p, a.loadSlugMap(ctx, channelID))
	if err != nil {
		return nil, err
	}
	meta := manifest.FileMeta{
		CanonicalPath: p,
		DisplayName:   displayName,
		ParentHuman:   fsmodel.HumanParent(p),
		Size:          size,
		Hash:          contentHash,
		MIME:          mimeType,
		Tags:          tags,
	}
	capRes, err := manifest.RenderCaption(meta, a.Cfg.Caption.SafeMediaCaptionUTF16Units, a.Cfg.Caption.MarginUTF16Units)
	if err != nil {
		return nil, err
	}
	if err := a.TG.EditCaption(ctx, tgChID, int(messageID.Int64), capRes.Caption); err != nil {
		if _, ok := err.(*telegram.MessageNotEditableError); !ok {
			return nil, mapTGErr(err)
		}
		// Caption text may already match; manifests can still be repaired.
	}
	newManifestID := manifestID
	if capRes.NeedsManifestReply {
		if manifestID.Valid {
			if err := a.TG.EditText(ctx, tgChID, int(manifestID.Int64), capRes.ManifestReply); err != nil {
				return nil, mapTGErr(err)
			}
		} else {
			id, err := a.TG.SendTextReply(ctx, tgChID, int(messageID.Int64), capRes.ManifestReply)
			if err != nil {
				return nil, mapTGErr(err)
			}
			newManifestID = sql.NullInt64{Int64: int64(id), Valid: true}
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	err = a.DB.WithTx(ctx, func(tx *sql.Tx) error {
		var mfID any
		if newManifestID.Valid {
			mfID = newManifestID.Int64
		}
		if _, err := tx.ExecContext(ctx, `update files set manifest_message_id=?, updated_at=? where id=?`, mfID, now, fileID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `delete from path_tags where file_id=?`, fileID); err != nil {
			return err
		}
		for i, tag := range capRes.IncludedTags {
			if _, err := tx.ExecContext(ctx, `insert into path_tags(file_id,tag,depth) values(?,?,?)`, fileID, tag, i); err != nil {
				return err
			}
		}
		for _, sm := range slugMaps {
			if _, err := tx.ExecContext(ctx, `insert or ignore into path_segment_slugs(channel_id,parent_canonical_path,segment,slug,hash_len,created_at) values(?,?,?,?,?,?)`,
				channelID, sm.ParentCanonical, sm.Segment, sm.Slug, sm.HashLen, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "repair path", err)
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

// UploadRecursive uploads a directory recursively.
func (a *App) UploadRecursive(ctx context.Context, localDir, remoteDir string, policy ConflictPolicy, continueOnError, noHash, includeEmptyDirs bool) (map[string]any, error) {
	if includeEmptyDirs {
		return nil, apperr.New(apperr.ErrEmptyDirsUnsupported, "empty directories cannot be persisted to Telegram in V1")
	}
	remoteDir, err := fsmodel.NormalizeCanonicalPath(remoteDir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(localDir)
	if err != nil {
		return nil, apperr.New(apperr.ErrLocalNotFound, localDir)
	}
	if !info.IsDir() {
		return nil, apperr.New(apperr.ErrUsage, "recursive upload requires a directory source")
	}
	var files []string
	err = filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrLocalNotFound, "walk local directory", err)
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

func (a *App) indexScannedFile(ctx context.Context, channelID int64, messageID int, manifestMsgID *int, meta manifest.ParsedMeta, now string) error {
	var mfID any
	if manifestMsgID != nil {
		mfID = *manifestMsgID
	}
	// Resolve the row this message maps to: first by message_id (a message
	// belongs to exactly one row), then by a reactivatable row at the path.
	// Superseded/deleted history rows are never resurrected by path — that
	// would collide with the live row after a --replace or re-upload.
	var fileID int64
	err := a.DB.Raw().QueryRowContext(ctx, `
		select id from files where channel_id=? and message_id=?`,
		channelID, messageID).Scan(&fileID)
	if err == sql.ErrNoRows {
		err = a.DB.Raw().QueryRowContext(ctx, `
			select id from files where channel_id=? and canonical_path=? and status in ('active','missing')`,
			channelID, meta.CanonicalPath).Scan(&fileID)
	}
	if err != nil && err != sql.ErrNoRows {
		return apperr.Wrap(apperr.ErrDB, "lookup scanned file", err)
	}
	// Duplicate-claim guard: another live message already holds this path.
	// The newest message wins; the older duplicate is recorded as a scan error.
	var otherID int64
	var otherMsg sql.NullInt64
	dupErr := a.DB.Raw().QueryRowContext(ctx, `
		select id, message_id from files where channel_id=? and canonical_path=? and status='active' and id != ?`,
		channelID, meta.CanonicalPath, fileID).Scan(&otherID, &otherMsg)
	if dupErr == nil && otherID != 0 {
		if otherMsg.Valid && int(otherMsg.Int64) > messageID {
			_, _ = a.DB.Raw().ExecContext(ctx, `
				insert into scan_errors(channel_id,message_id,error_code,error_message,raw_excerpt,status,first_seen_at,last_seen_at)
				values(?,?,?,?,?,'pending',?,?)
				on conflict(channel_id,message_id,error_code) do update set last_seen_at=excluded.last_seen_at, status='pending'`,
				channelID, messageID, apperr.ErrPathConflict,
				fmt.Sprintf("older duplicate of %s (newer message %d wins)", meta.CanonicalPath, otherMsg.Int64),
				meta.CanonicalPath, now, now)
			return nil
		}
		_, _ = a.DB.Raw().ExecContext(ctx, `update files set status='superseded', node_id=null, updated_at=? where id=?`, now, otherID)
	}
	if fileID != 0 {
		_, err = a.DB.Raw().ExecContext(ctx, `
			update files set message_id=?, manifest_message_id=?, canonical_path=?, display_name=?, size=?, content_hash=?, mime=?, status='active', updated_at=?
			where id=?`,
			messageID, mfID, meta.CanonicalPath, meta.DisplayName, meta.Size, meta.Hash, meta.MIME, now, fileID)
	} else {
		_, err = a.DB.Raw().ExecContext(ctx, `
			insert into files(channel_id,message_id,manifest_message_id,canonical_path,display_name,size,content_hash,mime,status,uploaded_at,updated_at)
			values(?,?,?,?,?,?,?,?,'active',?,?)`,
			channelID, messageID, mfID, meta.CanonicalPath, meta.DisplayName, meta.Size, meta.Hash, meta.MIME, now, now)
	}
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "index scanned file", err)
	}
	if fileID == 0 {
		_ = a.DB.Raw().QueryRowContext(ctx, `select id from files where channel_id=? and canonical_path=? and status='active'`,
			channelID, meta.CanonicalPath).Scan(&fileID)
	}
	tags, slugMaps, err := pathcodec.GenerateChain(meta.CanonicalPath, a.loadSlugMap(ctx, channelID))
	if err != nil {
		return err
	}
	for _, sm := range slugMaps {
		_, _ = a.DB.Raw().ExecContext(ctx, `insert or ignore into path_segment_slugs(channel_id,parent_canonical_path,segment,slug,hash_len,created_at) values(?,?,?,?,?,?)`,
			channelID, sm.ParentCanonical, sm.Segment, sm.Slug, sm.HashLen, now)
	}
	_ = a.DB.Raw().QueryRowContext(ctx, `select id from files where channel_id=? and canonical_path=? and status='active'`, channelID, meta.CanonicalPath).Scan(&fileID)
	for i, tag := range tags {
		_, _ = a.DB.Raw().ExecContext(ctx, `insert or ignore into path_tags(file_id,tag,depth) values(?,?,?)`, fileID, tag, i)
	}
	for anc, name := range fsmodel.DeriveDirectoryNodes([]string{meta.CanonicalPath}) {
		_, _ = a.DB.Raw().ExecContext(ctx, `insert or ignore into nodes(channel_id,canonical_path,parent_path,display_name,type,derived,created_at,updated_at) values(?,?,?,?,'dir',1,?,?)`,
			channelID, anc, fsmodel.ParentPath(anc), name, now, now)
	}
	return nil
}
