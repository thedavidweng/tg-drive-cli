package service

import (
	"context"
	"errors"
	"strings"
	"time"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/pathcodec"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

type captionRepairItem struct {
	Path      string `json:"path"`
	MessageID int    `json:"message_id"`
	Action    string `json:"action"`
	Reason    string `json:"reason,omitempty"`
}

type captionRepairTarget struct {
	path      string
	messageID int
	display   string
}

// RepairCaptions removes td's former parent-path and path-hashtag scaffold
// from modern captions. The exact path, hash, MIME, and tag records remain in
// the discussion manifest, so this operation only changes the human surface.
// Legacy rows are excluded because their captions may still carry td:v1.
func (a *App) RepairCaptions(ctx context.Context, remotePath string, dryRun, continueOnError bool) (map[string]any, error) {
	channelID, _, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	tgChannelID, err := a.tgChannelID(ctx)
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
	inRoot := func(path string) bool {
		return root == "/" || path == root || strings.HasPrefix(path, root+"/")
	}

	rows, err := a.DB.Raw().QueryContext(ctx, `
		select canonical_path, message_id, display_name
		from files
		where channel_id=? and status='active' and message_id is not null
		  and manifest_chat_tg_id <> ''
		order by canonical_path`, channelID)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "list modern captions", err)
	}
	var targets []captionRepairTarget
	for rows.Next() {
		var target captionRepairTarget
		if err := rows.Scan(&target.path, &target.messageID, &target.display); err != nil {
			_ = rows.Close()
			return nil, apperr.Wrap(apperr.ErrDB, "scan modern caption", err)
		}
		if inRoot(target.path) {
			targets = append(targets, target)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, apperr.Wrap(apperr.ErrDB, "list modern captions", err)
	}
	_ = rows.Close()

	existingSlugs := a.loadSlugMap(ctx, channelID)
	items := make([]captionRepairItem, 0, len(targets))
	cleaned, planned, skipped, failed := 0, 0, 0, 0
	for _, target := range targets {
		item := captionRepairItem{
			Path:      target.path,
			MessageID: target.messageID,
		}
		tags, _, tagErr := pathcodec.GenerateChain(target.path, existingSlugs)
		if tagErr != nil {
			item.Action = "failed"
			item.Reason = tagErr.Error()
			items = append(items, item)
			failed++
			if !continueOnError {
				return nil, tagErr
			}
			continue
		}

		repairErr := a.withLocks(ctx, lockKeysForPaths(channelID, target.path), func(ctx context.Context) error {
			msg, err := a.TG.GetMessage(ctx, tgChannelID, target.messageID)
			if err != nil {
				return telegram.MapError(err)
			}
			if msg.Caption == "" {
				item.Action = "skipped"
				item.Reason = "empty caption"
				return nil
			}
			if manifest.HasMachineMeta(msg.Caption) {
				item.Action = "skipped"
				item.Reason = "machine caption"
				return nil
			}
			clean, changed := manifest.StripRenderedScaffold(
				msg.Caption,
				target.display,
				fsmodel.HumanParent(target.path),
				tags,
			)
			if !changed {
				item.Action = "skipped"
				item.Reason = "already clean or scaffold not exact"
				return nil
			}
			if dryRun {
				item.Action = "would_clean"
				return nil
			}
			tx, err := a.DB.Raw().BeginTx(ctx, nil)
			if err != nil {
				return apperr.Wrap(apperr.ErrDB, "begin caption cleanup", err)
			}
			if err := a.TG.EditCaption(ctx, tgChannelID, target.messageID, clean); err != nil {
				_ = tx.Rollback()
				var notEditable *telegram.MessageNotEditableError
				if errors.As(err, &notEditable) {
					item.Action = "skipped"
					item.Reason = "message not editable"
					return nil
				}
				return telegram.MapError(err)
			}
			if _, err := tx.ExecContext(ctx,
				`update files set updated_at=? where channel_id=? and canonical_path=? and status='active'`,
				time.Now().UTC().Format(time.RFC3339), channelID, target.path); err != nil {
				_ = tx.Rollback()
				return apperr.Wrap(apperr.ErrDB, "record caption cleanup", err)
			}
			if err := tx.Commit(); err != nil {
				return apperr.Wrap(apperr.ErrDB, "commit caption cleanup", err)
			}
			item.Action = "clean"
			return nil
		})
		if repairErr != nil {
			item.Action = "failed"
			item.Reason = repairErr.Error()
			failed++
			items = append(items, item)
			if !continueOnError {
				return nil, repairErr
			}
			continue
		}
		switch item.Action {
		case "clean":
			cleaned++
		case "would_clean":
			planned++
		case "skipped":
			skipped++
		}
		items = append(items, item)
	}
	return map[string]any{
		"dry_run": dryRun,
		"cleaned": cleaned,
		"planned": planned,
		"skipped": skipped,
		"failed":  failed,
		"total":   len(targets),
		"items":   items,
	}, nil
}
