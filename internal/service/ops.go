package service

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	"github.com/thedavidweng/tg-drive-cli/core/publisher"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"lukechampine.com/blake3"
)

// LSEntry is one directory listing entry.
//
// The td ls --json wire shape is contract-tested: file entries always carry
// hash (see MarshalJSON); dir entries keep their historical key set.
type LSEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size,omitempty"`
	// Hash is the stored BLAKE3 content hash ("blake3:<hex>"); empty when
	// unknown, e.g. rows adopted without --hash. Emitted only for file
	// entries, where it is always present (possibly "") so no-download
	// integrity audits can rely on the key.
	Hash      string `json:"-"`
	Status    string `json:"status,omitempty"`
	Ephemeral bool   `json:"ephemeral,omitempty"`
}

// lsFileJSON is the wire shape of a file entry in ls --json output.
type lsFileJSON struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Type      string `json:"type"`
	Size      int64  `json:"size,omitempty"`
	Hash      string `json:"hash"`
	Status    string `json:"status,omitempty"`
	Ephemeral bool   `json:"ephemeral,omitempty"`
}

// lsDirJSON is the wire shape of a directory entry; it predates the hash
// field and must not grow one.
type lsDirJSON LSEntry

// MarshalJSON pins the ls --json entry contract: file entries always carry
// hash — the stored blake3 value, "" when unknown — while dir entries keep
// their historical key set.
func (e LSEntry) MarshalJSON() ([]byte, error) {
	if e.Type == "file" {
		return json.Marshal(lsFileJSON(e))
	}
	return json.Marshal(lsDirJSON(e))
}

// ListDir lists children of a remote path.
func (a *App) ListDir(ctx context.Context, remotePath string) ([]LSEntry, error) {
	// Trailing slashes are accepted directory intent; canonical paths drop
	// them (fsmodel rejects them outright).
	p, err := fsmodel.NormalizeCanonicalPath(strings.TrimRight(remotePath, "/"))
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
		select canonical_path, display_name, 'file' as type, coalesce(size,0), status, 0, coalesce(content_hash,'')
		from files where channel_id=? and status='active' and canonical_path like ? escape '\'
		union
		select canonical_path, display_name, 'dir', 0, '', ephemeral, ''
		from nodes where channel_id=? and type='dir' and parent_path=?
		order by type desc, display_name`, channelID, escapeLike(prefix)+"%", channelID, p)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "ls", err)
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]bool{}
	var out []LSEntry
	for rows.Next() {
		var fullPath, name, typ, status, contentHash string
		var size int64
		var ephemeral int
		if err := rows.Scan(&fullPath, &name, &typ, &size, &status, &ephemeral, &contentHash); err != nil {
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
		entry := LSEntry{Name: childName, Path: fullPath, Type: typ, Size: size, Status: status, Ephemeral: ephemeral == 1}
		if typ == "file" {
			entry.Hash = contentHash
		}
		out = append(out, entry)
	}
	if len(out) == 0 && p != "/" {
		// Nothing under p: it is either a file (list it, like Unix ls), an
		// empty directory (empty listing), or absent (error).
		var name, status, contentHash string
		var size int64
		err := a.DB.Raw().QueryRowContext(ctx, `select display_name, coalesce(size,0), status, coalesce(content_hash,'') from files where channel_id=? and canonical_path=? and status='active'`, channelID, p).Scan(&name, &size, &status, &contentHash)
		switch err {
		case nil:
			return []LSEntry{{Name: name, Path: p, Type: "file", Size: size, Hash: contentHash, Status: status}}, nil
		case sql.ErrNoRows:
			var one int
			dirErr := a.DB.Raw().QueryRowContext(ctx, `select 1 from nodes where channel_id=? and canonical_path=? and type='dir'`, channelID, p).Scan(&one)
			if dirErr == sql.ErrNoRows {
				return nil, apperr.New(apperr.ErrRemoteNotFound, fmt.Sprintf("remote path %q not found", p))
			}
			if dirErr != nil {
				return nil, apperr.Wrap(apperr.ErrDB, "ls", dirErr)
			}
		default:
			return nil, apperr.Wrap(apperr.ErrDB, "ls", err)
		}
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
	// Persisted resumable-upload states: abandoned attempts are collectable
	// via td repair --pending.
	if uploadStates, err := a.DB.CountUploadStates(ctx); err == nil {
		out["upload_states"] = uploadStates
	}
	var staleLocks int
	_ = a.DB.Raw().QueryRowContext(ctx, `select count(*) from operation_locks where expires_at < ?`, nowT.Format(time.RFC3339)).Scan(&staleLocks)
	out["stale_locks"] = staleLocks
	out["orphaned"] = counts["orphaned"]
	out["upload_limit_bytes"] = a.uploadLimit(ctx)
	return out, nil
}

// DownloadResult reports what DownloadFile actually did.
type DownloadResult struct {
	Path    string `json:"path"`    // remote canonical path
	Dest    string `json:"local"`   // local destination actually written
	Size    int64  `json:"size"`    // remote size in bytes
	Skipped bool   `json:"skipped"` // destination existed and --skip-existing was set
}

// DownloadFile downloads a remote file to local path, streaming through a
// temp file and verifying size/hash before the atomic rename.
func (a *App) DownloadFile(ctx context.Context, remotePath, localDest string, policy ConflictPolicy) (*DownloadResult, error) {
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
	var messageID int
	var size int64
	var hash string
	err = a.DB.Raw().QueryRowContext(ctx, `select message_id, size, content_hash from files where channel_id=? and canonical_path=? and status='active'`, channelID, p).Scan(&messageID, &size, &hash)
	if err == sql.ErrNoRows {
		return nil, apperr.New(apperr.ErrRemoteNotFound, fmt.Sprintf("remote path %q not found", p))
	}
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "lookup file", err)
	}
	// cp convention: a destination that ends with a separator or names an
	// existing directory keeps the remote file's basename.
	if strings.HasSuffix(localDest, "/") || strings.HasSuffix(localDest, string(os.PathSeparator)) {
		localDest = filepath.Join(localDest, fsmodel.BaseName(p))
	} else if info, err := a.files().Stat(ctx, localDest); err == nil && info.IsDir {
		localDest = filepath.Join(localDest, fsmodel.BaseName(p))
	}
	if _, err := a.files().Stat(ctx, localDest); err == nil {
		switch policy {
		case ConflictSkip:
			return &DownloadResult{Path: p, Dest: localDest, Size: size, Skipped: true}, nil
		case ConflictReplace:
		case ConflictRename:
			localDest = autoRenameLocal(ctx, a.files(), localDest)
		default:
			return nil, apperr.New(apperr.ErrLocalPathExists,
				fmt.Sprintf("local file %q already exists (use --replace, --skip-existing, or --auto-rename)", localDest))
		}
	}
	if err := a.files().MkdirAll(ctx, filepath.Dir(localDest), 0o755); err != nil {
		return nil, err
	}
	tmp, f, err := a.files().CreateTemp(ctx, localDest)
	if err != nil {
		return nil, err
	}
	hashEnabled := hash != "" && a.Cfg.Hash.Enabled && strings.HasPrefix(hash, "blake3:")
	var w io.Writer = f
	var h *blake3.Hasher
	if hashEnabled {
		h = blake3.New(32, nil)
		w = io.MultiWriter(f, h)
	}
	nativePhoto, err := a.downloadTo(ctx, tgChID, messageID, w)
	if err != nil {
		_ = f.Close()
		_ = a.files().Remove(ctx, tmp)
		return nil, telegram.MapError(err)
	}
	if err := f.Close(); err != nil {
		_ = a.files().Remove(ctx, tmp)
		return nil, err
	}
	// Native photos are Telegram's own recompressed representations: the
	// stored size/hash describe the original upload bytes, which the platform
	// never serves back (docs/integration-notes.md). Documents and attributed
	// videos keep strict verification because their bytes are untouched.
	if !nativePhoto {
		if size > 0 {
			info, err := a.files().Stat(ctx, tmp)
			if err != nil {
				_ = a.files().Remove(ctx, tmp)
				return nil, err
			}
			if info.Size != size {
				_ = a.files().Remove(ctx, tmp)
				return nil, apperr.New(apperr.ErrTelegramRPC, "size mismatch")
			}
		}
		if hashEnabled {
			got := "blake3:" + hex.EncodeToString(h.Sum(nil))
			if got != hash {
				_ = a.files().Remove(ctx, tmp)
				return nil, apperr.New(apperr.ErrTelegramRPC, "content hash mismatch")
			}
		}
	}
	if err := a.files().Rename(ctx, tmp, localDest); err != nil {
		_ = a.files().Remove(ctx, tmp)
		return nil, err
	}
	return &DownloadResult{Path: p, Dest: localDest, Size: size}, nil
}

// downloadTo streams the message's downloadable body. It reports whether the
// message is a native photo: Telegram serves its own recompressed
// representation for photos, so the stored size/hash of the original bytes
// cannot hold for what comes back.
func (a *App) downloadTo(ctx context.Context, tgChID int64, messageID int, w io.Writer) (bool, error) {
	nativePhoto := false
	if msg, err := a.TG.GetMessage(ctx, tgChID, messageID); err == nil {
		if msg.Kind == telegram.KindText || (msg.MIME == "text/plain" && len(msg.Data) == 0 && msg.FileName == "") {
			body := msg.Text
			if body == "" {
				body = msg.Caption
			}
			_, err := w.Write([]byte(manifest.SplitHumanAndMachine(body)))
			return false, err
		}
		nativePhoto = msg.Kind == telegram.KindPhoto
	}
	err := a.TG.DownloadMedia(ctx, tgChID, messageID, w)
	return nativePhoto, err
}

func autoRenameLocal(ctx context.Context, files ports.FileSystem, path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	for i := 1; i < 1000; i++ {
		candidate := filepath.Join(dir, fsmodel.ConflictRenameCandidate(base, i))
		if _, err := files.Stat(ctx, candidate); errors.Is(err, fs.ErrNotExist) {
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
	var manifestChat string
	var displayName, contentHash, mimeType string
	var size int64
	err = a.DB.Raw().QueryRowContext(ctx, `select id, message_id, manifest_message_id, manifest_chat_tg_id, display_name, size, content_hash, mime from files where channel_id=? and canonical_path=? and status='active'`,
		channelID, src).Scan(&fileID, &messageID, &manifestID, &manifestChat, &displayName, &size, &contentHash, &mimeType)
	if err == sql.ErrNoRows {
		var otherChannel int64
		if scanErr := a.DB.Raw().QueryRowContext(ctx, `select channel_id from files where canonical_path=? and status='active' and channel_id != ? limit 1`,
			src, channelID).Scan(&otherChannel); scanErr == nil {
			return apperr.New(apperr.ErrCrossChannelMove, "source file belongs to a different channel; V1 supports same-channel moves only")
		}
		return apperr.New(apperr.ErrRemoteNotFound, fmt.Sprintf("remote path %q not found", src))
	}
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "lookup source", err)
	}
	// Path locks (sorted) with heartbeat renewal: a move touching two paths
	// holds both for the whole operation regardless of duration.
	lockErr := a.withLocks(ctx, lockKeysForPaths(channelID, src, dst), func(ctx context.Context) error {
		existingSlugs, err := a.loadExistingSlugs(ctx, channelID)
		if err != nil {
			return err
		}
		oldTags, _, err := pathcodec.GenerateChain(src, existingSlugs)
		if err != nil {
			return err
		}
		if !messageID.Valid {
			return apperr.New(apperr.ErrRemoteNotFound, fmt.Sprintf("remote path %q not found", src))
		}

		oldMeta := manifest.FileMeta{
			CanonicalPath: src,
			DisplayName:   displayName,
			ParentHuman:   fsmodel.HumanParent(src),
			Size:          size,
			Hash:          contentHash,
			MIME:          mimeType,
			Tags:          oldTags,
		}
		meta := manifest.FileMeta{
			CanonicalPath: dst,
			DisplayName:   fsmodel.BaseName(dst),
			ParentHuman:   fsmodel.HumanParent(dst),
			Size:          size,
			Hash:          contentHash,
			MIME:          mimeType,
		}
		manifestMsgID := 0
		if manifestID.Valid {
			manifestMsgID = int(manifestID.Int64)
		}

		carrier := a.manifestCarrier(manifestChat)
		if album, ok, err := a.loadAlbumManifest(ctx, tgChID, carrier, manifestMsgID); err != nil {
			return telegram.MapError(err)
		} else if ok {
			updated := albumReplacePath(album, int(messageID.Int64), dst)
			if _, err := a.writeAlbumManifest(ctx, channelID, tgChID, carrier, manifestMsgID, albumFirstMediaID(updated), updated); err != nil {
				return err
			}
			return a.reindexAlbumMember(ctx, channelID, fileID, int(messageID.Int64), manifestMsgID, dst, contentHash, mimeType, size)
		}

		if _, err := a.publisher().Publish(ctx, publisher.PublishRequest{
			ChannelRowID:   channelID,
			ChannelID:      tgChID,
			FileID:         fileID,
			MessageID:      int(messageID.Int64),
			ManifestMsgID:  manifestMsgID,
			ManifestChatID: manifestChat,
			Meta:           meta,
			ExistingSlugs:  existingSlugs,
			EditCaption:    true,
			OldMeta:        &oldMeta,
		}); err != nil {
			return err
		}
		return a.DB.RunDirectoryGC(ctx, channelID)
	})
	return lockErr
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
	var manifestChat string
	err = a.DB.Raw().QueryRowContext(ctx, `select id, message_id, manifest_message_id, manifest_chat_tg_id from files where channel_id=? and canonical_path=? and status='active'`, channelID, p).Scan(&fileID, &messageID, &manifestID, &manifestChat)
	if err == sql.ErrNoRows {
		return nil, apperr.New(apperr.ErrRemoteNotFound, fmt.Sprintf("remote path %q not found", p))
	}
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "lookup file", err)
	}
	// Hold the path lock (with heartbeat renewal) for the whole delete: the
	// Telegram mutations and the index commit must be exclusive.
	lockKey := sqlitestore.LockKey(channelID, p)
	var out map[string]any
	lockErr := a.withLocks(ctx, []string{lockKey}, func(ctx context.Context) error {
		res, err := a.deleteFileLocked(ctx, channelID, tgChID, p, fileID, messageID, manifestID, manifestChat, opts)
		if err != nil {
			return err
		}
		out = res
		return nil
	})
	if lockErr != nil {
		return nil, lockErr
	}
	return out, nil
}

func (a *App) deleteFileLocked(ctx context.Context, channelID, tgChID int64, p string, fileID int64, messageID, manifestID sql.NullInt64, manifestChat string, opts DeleteOptions) (map[string]any, error) {
	mode := a.Cfg.Delete.Mode
	if opts.Tombstone {
		mode = "tombstone"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	carrier := a.manifestCarrier(manifestChat)
	var manifestErr error
	manID := 0
	if manifestID.Valid {
		manID = int(manifestID.Int64)
	}
	if album, ok, err := a.loadAlbumManifest(ctx, tgChID, carrier, manID); err != nil && !isMessageGone(err) {
		return nil, telegram.MapError(err)
	} else if ok {
		if messageID.Valid {
			if err := a.TG.DeleteMessage(ctx, tgChID, int(messageID.Int64)); err != nil && !isMessageGone(err) {
				return nil, telegram.MapError(err)
			}
		}
		remaining := albumWithout(album, int(messageID.Int64))
		if len(remaining.Files) == 0 {
			manifestErr = carrier.Delete(ctx, tgChID, manID)
		} else {
			_, manifestErr = a.writeAlbumManifest(ctx, channelID, tgChID, carrier, manID, albumFirstMediaID(remaining), remaining)
		}
		if isMessageGone(manifestErr) {
			manifestErr = nil
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
		out := map[string]any{"path": p, "mode": "delete"}
		if manifestErr != nil {
			out["stale_manifest"] = true
			if !opts.AllowStaleManifest {
				return out, apperr.New(apperr.ErrTelegramRPC,
					fmt.Sprintf("album inventory %d could not be updated: %v; rerun with --allow-stale-manifest to ignore", manID, manifestErr))
			}
		}
		return out, nil
	}
	switch {
	case mode == "delete":
		if messageID.Valid {
			if err := a.TG.DeleteMessage(ctx, tgChID, int(messageID.Int64)); err != nil && !isMessageGone(err) {
				return nil, telegram.MapError(err)
			}
		}
		if manifestID.Valid {
			manifestErr = carrier.Delete(ctx, tgChID, int(manifestID.Int64))
		}
	case manifestID.Valid && carrier.Comment():
		// ADR 0018: the comment carries the tombstone. If the comment edit
		// fails, fall back to a tombstone caption — deletion must stay
		// sticky even when the thread record cannot be redacted, and a
		// caption tombstone outranks a stale live comment during scans.
		manifestErr = carrier.Edit(ctx, tgChID, int(manifestID.Int64), manifest.RenderTombstoneManifest(p))
		if manifestErr != nil && !isMessageGone(manifestErr) {
			if messageID.Valid {
				if capErr := a.TG.EditCaption(ctx, tgChID, int(messageID.Int64), manifest.RenderTombstoneCaption(fsmodel.BaseName(p), p)); capErr == nil || isMessageGone(capErr) {
					manifestErr = nil
				}
			}
		}
	default:
		if messageID.Valid {
			if err := a.TG.EditCaption(ctx, tgChID, int(messageID.Int64), manifest.RenderTombstoneCaption(fsmodel.BaseName(p), p)); err != nil && !isMessageGone(err) {
				return nil, telegram.MapError(err)
			}
		}
		if manifestID.Valid {
			manifestErr = carrier.Edit(ctx, tgChID, int(manifestID.Int64), manifest.RenderTombstoneManifest(p))
		}
	}
	if isMessageGone(manifestErr) {
		manifestErr = nil
	}
	// Media mutation already succeeded (or the message was already gone).
	// Commit deleted even if the manifest reply cannot be redacted.
	err := a.DB.WithTx(ctx, func(tx *sql.Tx) error {
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
		if !opts.AllowStaleManifest {
			return out, apperr.New(apperr.ErrTelegramRPC,
				fmt.Sprintf("manifest reply %d could not be redacted: %v; rerun with --allow-stale-manifest to ignore", manifestID.Int64, manifestErr))
		}
	}
	return out, nil
}

// UploadRecursive uploads a directory recursively. Files are published as
// native media groups: each source directory's direct children form one
// album, split into consecutive groups of MaxMediaGroupMembers (issue #26).
func (a *App) UploadRecursive(ctx context.Context, localDir, remoteDir string, policy ConflictPolicy, continueOnError, noHash, includeEmptyDirs bool) (map[string]any, error) {
	if includeEmptyDirs {
		return nil, apperr.New(apperr.ErrEmptyDirsUnsupported, "empty directories cannot be persisted to Telegram in V1")
	}
	// Trailing slashes are directory intent; the canonical form drops them.
	remoteDir, err := fsmodel.NormalizeCanonicalPath(strings.TrimRight(remoteDir, "/"))
	if err != nil {
		return nil, err
	}
	info, err := a.files().Stat(ctx, localDir)
	if err != nil {
		return nil, apperr.New(apperr.ErrLocalNotFound, fmt.Sprintf("local directory %q not found", localDir))
	}
	if !info.IsDir {
		return nil, apperr.New(apperr.ErrUsage, "recursive upload requires a directory source")
	}
	var files []string
	err = a.files().Walk(ctx, localDir, func(path string, info ports.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrLocalNotFound, "walk local directory", err)
	}
	sort.Strings(files)

	channelID, tgIDStr, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	tgChID, err := a.tgChannelID(ctx)
	if err != nil {
		return nil, err
	}

	uploaded, skipped, failed := 0, 0, 0
	var errs []string
	albums := []AlbumGroup{}
	// Each source directory's children become one album batch.
	groups := map[string][]albumSource{}
	order := []string{}
	for _, f := range files {
		dir := filepath.Dir(f)
		if _, ok := groups[dir]; !ok {
			order = append(order, dir)
		}
		rel, _ := filepath.Rel(localDir, f)
		dest := remoteDir
		if dest != "/" {
			dest += "/"
		}
		dest += filepath.ToSlash(rel)
		groups[dir] = append(groups[dir], albumSource{localPath: f, dest: dest})
	}
	sort.Strings(order)
	for _, dir := range order {
		batch, failures, err := a.planAlbumBatch(ctx, groups[dir], policy, noHash, Presentation{}, continueOnError)
		failed += len(failures)
		errs = append(errs, failures...)
		if err != nil && !continueOnError {
			return nil, err
		}
		if err != nil {
			continue
		}
		skipped += batch.skipped
		if len(batch.members) == 0 {
			continue
		}
		data, err := a.runAlbumBatch(ctx, batch, channelID, tgChID, tgIDStr)
		if err != nil {
			failed++
			errs = append(errs, err.Error())
			if !continueOnError {
				return nil, err
			}
			continue
		}
		uploaded += data["uploaded"].(int)
		if gs, ok := data["albums"].([]AlbumGroup); ok {
			albums = append(albums, gs...)
		}
	}

	data := map[string]any{"uploaded": uploaded, "skipped": skipped, "failed": failed, "errors": errs, "albums": albums}
	if link, err := a.TG.GetInviteLink(ctx, tgChID); err == nil && link != "" {
		data["invite_link"] = link
		data["channel_id"] = fmt.Sprintf("%d", tgChID)
	}
	return data, nil
}

// DownloadRecursive downloads a directory tree and reports per-file results.
func (a *App) DownloadRecursive(ctx context.Context, remotePath, localDir string, policy ConflictPolicy, continueOnError bool) (map[string]any, error) {
	st := &downloadStats{}
	if err := a.downloadRecursive(ctx, remotePath, localDir, policy, continueOnError, st); err != nil {
		return nil, err
	}
	return map[string]any{
		"path":       remotePath,
		"local":      localDir,
		"downloaded": st.downloaded,
		"skipped":    st.skipped,
		"failed":     st.failed,
		"errors":     st.errors,
	}, nil
}

type downloadStats struct {
	downloaded int
	skipped    int
	failed     int
	errors     []string
}

func (a *App) downloadRecursive(ctx context.Context, remotePath, localDir string, policy ConflictPolicy, continueOnError bool, st *downloadStats) error {
	entries, err := a.ListDir(ctx, remotePath)
	if err != nil {
		return err
	}
	for _, e := range entries {
		localPath := filepath.Join(localDir, e.Name)
		if e.Type == "dir" {
			if err := a.files().MkdirAll(ctx, localPath, 0o755); err != nil {
				st.failed++
				st.errors = append(st.errors, err.Error())
				if !continueOnError {
					return err
				}
				continue
			}
			if err := a.downloadRecursive(ctx, e.Path, localPath, policy, continueOnError, st); err != nil && !continueOnError {
				return err
			}
			continue
		}
		res, err := a.DownloadFile(ctx, e.Path, localPath, policy)
		if err != nil {
			st.failed++
			st.errors = append(st.errors, err.Error())
			if !continueOnError {
				return err
			}
			continue
		}
		if res.Skipped {
			st.skipped++
			continue
		}
		st.downloaded++
	}
	return nil
}
