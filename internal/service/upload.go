package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	"github.com/thedavidweng/tg-drive-cli/core/drive"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/model"
	"github.com/thedavidweng/tg-drive-cli/core/pathcodec"
	"github.com/thedavidweng/tg-drive-cli/core/ports"
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
	Render  func() bool // returns json mode
}

func (a *App) files() ports.FileSystem {
	if a.Runtime == nil || a.Runtime.Files == nil {
		panic("runtime filesystem not configured")
	}
	return a.Runtime.Files
}

// ConflictPolicy for uploads/downloads.
type ConflictPolicy string

const (
	ConflictFail    ConflictPolicy = "fail"
	ConflictReplace ConflictPolicy = "replace"
	ConflictSkip    ConflictPolicy = "skip"
	ConflictRename  ConflictPolicy = "rename"
)

func newOwnerToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (a *App) channelID(ctx context.Context) (int64, string, error) {
	var id int64
	var tgID, title string
	err := a.DB.Raw().QueryRowContext(ctx, `select id, tg_channel_id, title from channels limit 1`).Scan(&id, &tgID, &title)
	if err == sql.ErrNoRows {
		return 0, "", apperr.New(apperr.ErrChannelNotFound, "no channel configured; run td init")
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

func computeHash(path string, enabled bool) (string, error) {
	if !enabled {
		return "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := blake3.New(32, nil)
	if _, err := io.Copy(h, f); err != nil {
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

func (a *App) uploadLimit(ctx context.Context) int64 {
	tgChID, err := a.tgChannelID(ctx)
	if err != nil {
		return a.Cfg.Limits.FreeUploadBytes
	}
	caps, err := a.TG.Doctor(ctx, tgChID)
	if err != nil || caps == nil || caps.MaxUploadBytes == 0 {
		return a.Cfg.Limits.FreeUploadBytes
	}
	return caps.MaxUploadBytes
}

// UploadFile uploads a single local file.
func (a *App) UploadFile(ctx context.Context, localPath, remotePath string, policy ConflictPolicy, noHash bool) (map[string]any, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		return nil, apperr.New(apperr.ErrLocalNotFound, localPath)
	}
	if info.IsDir() {
		return nil, apperr.New(apperr.ErrUsage, "use --recursive for directories")
	}
	limit := a.uploadLimit(ctx)
	if info.Size() > limit {
		return nil, apperr.New(apperr.ErrFileTooLarge, fmt.Sprintf("file exceeds %d bytes", limit))
	}
	dest, err := fsmodel.NormalizeCanonicalPath(remotePath)
	if err != nil {
		return nil, err
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
	for _, ap := range active {
		if ap.Canonical == dest && !ap.IsDir {
			switch policy {
			case ConflictSkip:
				return map[string]any{"path": dest, "skipped": true}, nil
			case ConflictReplace:
				break
			case ConflictRename:
				base := fsmodel.BaseName(dest)
				dir := fsmodel.ParentPath(dest)
				for i := 1; i < 1000; i++ {
					candidate := dir + "/" + strings.TrimSuffix(base, filepath.Ext(base)) + fmt.Sprintf(" (%d)", i) + filepath.Ext(base)
					candidate, _ = fsmodel.NormalizeCanonicalPath(candidate)
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
				return nil, apperr.New(apperr.ErrPathExists, dest)
			}
		}
	}
	if policy != ConflictReplace {
		if err := fsmodel.CheckUploadConflict(dest, active); err != nil {
			return nil, err
		}
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

	owner := newOwnerToken()
	lockKey := sqlitestore.LockKey(channelID, dest)
	ttl := time.Duration(a.Cfg.Locks.TTLSeconds) * time.Second
	if err := a.DB.AcquireLock(ctx, lockKey, owner, ttl); err != nil {
		return nil, err
	}
	defer func() { _ = a.DB.ReleaseLock(ctx, lockKey, owner) }()

	now := time.Now().UTC().Format(time.RFC3339)
	hashEnabled := a.Cfg.Hash.Enabled && !noHash
	contentHash, err := computeHash(localPath, hashEnabled)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrLocalNotFound, "hash file", err)
	}
	mimeType := detectMIME(localPath)
	displayName := fsmodel.BaseName(dest)

	existingSlugs, err := a.loadExistingSlugs(ctx, channelID)
	if err != nil {
		return nil, err
	}
	tags, slugMaps, err := pathcodec.GenerateChain(dest, existingSlugs)
	if err != nil {
		return nil, err
	}

	meta := manifest.FileMeta{
		CanonicalPath: dest,
		DisplayName:   displayName,
		ParentHuman:   fsmodel.HumanParent(dest),
		Size:          info.Size(),
		Hash:          contentHash,
		MIME:          mimeType,
		Created:       now,
		Tags:          tags,
	}
	capRes, err := manifest.RenderCaption(meta, a.Cfg.Caption.SafeMediaCaptionUTF16Units, a.Cfg.Caption.MarginUTF16Units)
	if err != nil {
		return nil, err
	}

	var fileID int64
	err = a.DB.WithTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `insert into files(channel_id,canonical_path,display_name,original_local_path,size,content_hash,mime,status,updated_at) values(?,?,?,?,?,?,?,'pending',?)`,
			channelID, dest, displayName, localPath, info.Size(), contentHash, mimeType, now)
		if err != nil {
			return err
		}
		fileID, _ = res.LastInsertId()
		return nil
	})
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "insert pending", err)
	}

	f, err := os.Open(localPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	up, err := a.TG.UploadMedia(ctx, telegram.UploadRequest{
		ChannelID: tgChID,
		Caption:   capRes.Caption,
		FileName:  displayName,
		MIME:      mimeType,
		Size:      info.Size(),
		Reader:    f,
	})
	if err != nil {
		_, _ = a.DB.Raw().ExecContext(ctx, `delete from files where id=?`, fileID)
		return nil, mapTGErr(err)
	}

	var manifestMsgID *int
	if capRes.NeedsManifestReply {
		id, err := a.TG.SendTextReply(ctx, tgChID, up.MessageID, capRes.ManifestReply)
		if err != nil {
			_ = a.TG.DeleteMessage(ctx, tgChID, up.MessageID)
			_, _ = a.DB.Raw().ExecContext(ctx, `update files set status='orphaned', message_id=?, updated_at=? where id=?`, up.MessageID, now, fileID)
			return nil, mapTGErr(err)
		}
		manifestMsgID = &id
	}

	err = a.DB.WithTx(ctx, func(tx *sql.Tx) error {
		var mfID any
		if manifestMsgID != nil {
			mfID = *manifestMsgID
		}
		if replaceFileID > 0 {
			if _, err := tx.ExecContext(ctx, `update files set status='superseded', node_id=null, updated_at=? where id=?`,
				now, replaceFileID); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `update files set status='active', message_id=?, manifest_message_id=?, uploaded_at=?, updated_at=? where id=?`,
			up.MessageID, mfID, now, now, fileID)
		if err != nil {
			return err
		}
		for _, sm := range slugMaps {
			_, err = tx.ExecContext(ctx, `insert or ignore into path_segment_slugs(channel_id,parent_canonical_path,segment,slug,hash_len,created_at) values(?,?,?,?,?,?)`,
				channelID, sm.ParentCanonical, sm.Segment, sm.Slug, sm.HashLen, now)
			if err != nil {
				return err
			}
		}
		for i, tag := range capRes.IncludedTags {
			_, err = tx.ExecContext(ctx, `insert into path_tags(file_id,tag,depth) values(?,?,?)`, fileID, tag, i)
			if err != nil {
				return err
			}
		}
		for anc, name := range fsmodel.DeriveDirectoryNodes([]string{dest}) {
			_, err = tx.ExecContext(ctx, `insert or ignore into nodes(channel_id,canonical_path,parent_path,display_name,type,derived,created_at,updated_at) values(?,?,?,?,'dir',1,?,?)`,
				channelID, anc, fsmodel.ParentPath(anc), name, now, now)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "commit upload", err)
	}

	if replaceFileID > 0 && a.Cfg.Delete.Mode == "delete" {
		if oldMsgID.Valid {
			_ = a.TG.DeleteMessage(ctx, tgChID, int(oldMsgID.Int64))
		}
		if oldManifestID.Valid {
			_ = a.TG.DeleteMessage(ctx, tgChID, int(oldManifestID.Int64))
		}
	}

	return map[string]any{
		"path":                dest,
		"channel_id":          tgIDStr,
		"message_id":          up.MessageID,
		"manifest_message_id": manifestMsgID,
		"size":                info.Size(),
		"hash":                contentHash,
	}, nil
}

func mapTGErr(err error) error {
	switch err.(type) {
	case *telegram.AuthRequiredError:
		return apperr.New(apperr.ErrAuthRequired, err.Error())
	case *telegram.FileTooLargeError:
		return apperr.New(apperr.ErrFileTooLarge, err.Error())
	case *telegram.PermissionDeniedError:
		return apperr.New(apperr.ErrChannelPermission, err.Error())
	case *telegram.FloodWaitError:
		return apperr.New(apperr.ErrTelegramRateLimited, err.Error())
	case *telegram.MessageNotEditableError:
		return apperr.New(apperr.ErrMessageNotEditable, err.Error())
	default:
		return apperr.Wrap(apperr.ErrTelegramRPC, "telegram", err)
	}
}
