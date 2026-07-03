package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/thedavidweng/tg-drive-cli/internal/apperr"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
	"github.com/thedavidweng/tg-drive-cli/internal/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/internal/pathcodec"
	"github.com/thedavidweng/tg-drive-cli/internal/telegram"
)

// AuthLogin performs interactive login.
func (a *App) AuthLogin(ctx context.Context, codeFn, passwordFn func() (string, error)) (map[string]any, error) {
	if a.Cfg.Telegram.APIID == 0 || a.Cfg.Telegram.APIHash == "" {
		return nil, apperr.New(apperr.ErrConfigMissing, "telegram.api_id and telegram.api_hash required")
	}
	phone := a.Cfg.Telegram.Phone
	if phone == "" {
		return nil, apperr.New(apperr.ErrConfigMissing, "telegram.phone required")
	}
	user, err := a.TG.Login(ctx, a.Cfg.Telegram.APIID, a.Cfg.Telegram.APIHash, phone, codeFn, passwordFn)
	if err != nil {
		return nil, mapTGErr(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = a.DB.Raw().ExecContext(ctx, `
		insert into accounts(tg_user_id,phone,display_name,created_at,updated_at) values(?,?,?,?,?)
		on conflict(tg_user_id) do update set phone=excluded.phone, display_name=excluded.display_name, updated_at=excluded.updated_at`,
		fmt.Sprintf("%d", user.ID), user.Phone, user.DisplayName, now, now)
	return map[string]any{
		"user_id":      user.ID,
		"display_name": user.DisplayName,
		"phone":        config.RedactValue("telegram.phone", user.Phone, false),
	}, nil
}

// AuthStatus returns authentication status.
func (a *App) AuthStatus(ctx context.Context) (map[string]any, error) {
	user, ok, err := a.TG.Status(ctx)
	if err != nil {
		return nil, mapTGErr(err)
	}
	if !ok {
		return map[string]any{"authenticated": false}, nil
	}
	return map[string]any{
		"authenticated": true,
		"user_id":       user.ID,
		"display_name":  user.DisplayName,
		"phone":         config.RedactValue("telegram.phone", user.Phone, false),
	}, nil
}

// AuthLogout logs out.
func (a *App) AuthLogout(ctx context.Context) error {
	return a.TG.Logout(ctx)
}

// InitRoot initializes a local root and optionally creates/binds a channel.
func (a *App) InitRoot(ctx context.Context, localRoot, channelTitle string, create, bind string) (map[string]any, error) {
	user, ok, err := a.TG.Status(ctx)
	if err != nil {
		return nil, mapTGErr(err)
	}
	if !ok {
		return nil, apperr.New(apperr.ErrAuthRequired, "login required")
	}
	var ch *telegram.Channel
	switch {
	case create != "":
		ch, err = a.TG.CreateChannel(ctx, create)
	case bind != "":
		ch, err = a.TG.BindChannel(ctx, bind)
	case channelTitle != "":
		ch, err = a.TG.ResolveChannel(ctx, channelTitle)
		if err != nil {
			ch, err = a.TG.CreateChannel(ctx, channelTitle)
		}
	default:
		return nil, apperr.New(apperr.ErrUsage, "channel title or --create-channel/--bind-channel required")
	}
	if err != nil {
		return nil, mapTGErr(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var accountID int64
	_ = a.DB.Raw().QueryRowContext(ctx, `select id from accounts where tg_user_id=?`, fmt.Sprintf("%d", user.ID)).Scan(&accountID)
	if accountID == 0 {
		res, err := a.DB.Raw().ExecContext(ctx, `insert into accounts(tg_user_id,phone,display_name,created_at,updated_at) values(?,?,?,?,?)`,
			fmt.Sprintf("%d", user.ID), user.Phone, user.DisplayName, now, now)
		if err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "insert account", err)
		}
		accountID, _ = res.LastInsertId()
	}
	_, err = a.DB.Raw().ExecContext(ctx, `
		insert into channels(account_id,tg_channel_id,access_hash,title,root_local_path,root_remote_path,strategy,created_at,updated_at)
		values(?,?,?,?,?,?,'single',?,?)
		on conflict(account_id, tg_channel_id) do update set title=excluded.title, access_hash=excluded.access_hash, root_local_path=excluded.root_local_path, updated_at=excluded.updated_at`,
		accountID, fmt.Sprintf("%d", ch.ID), fmt.Sprintf("%d", ch.AccessHash), ch.Title, localRoot, "/", now, now)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "insert channel", err)
	}
	a.initScan(ctx)
	return map[string]any{
		"channel_id":    ch.ID,
		"channel_title": ch.Title,
		"local_root":    localRoot,
	}, nil
}

func (a *App) initScan(ctx context.Context) {
	_, _ = a.Scan(ctx, ScanOptions{Full: true})
}

// Share returns invite link and hashtag for a path.
func (a *App) Share(ctx context.Context, remotePath string) (map[string]any, error) {
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
	link, err := a.TG.GetInviteLink(ctx, tgChID)
	if err != nil {
		return nil, mapTGErr(err)
	}
	var tag string
	_ = a.DB.Raw().QueryRowContext(ctx, `
		select pt.tag from path_tags pt join files f on f.id=pt.file_id
		where f.channel_id=? and f.canonical_path=? and f.status='active' order by pt.depth limit 1`, channelID, p).Scan(&tag)
	if tag == "" {
		existing, err := a.loadExistingSlugs(ctx, channelID)
		if err != nil {
			return nil, err
		}
		tags, _, _ := pathcodec.GenerateChain(p, existing)
		if len(tags) > 0 {
			tag = tags[len(tags)-1]
		}
	}
	return map[string]any{
		"path":        p,
		"invite_link": link,
		"hashtag":     tag,
	}, nil
}

// Doctor runs capability checks.
func (a *App) Doctor(ctx context.Context) (map[string]any, error) {
	checks := map[string]string{}
	user, ok, err := a.TG.Status(ctx)
	switch {
	case err != nil:
		checks["auth"] = "fail"
	case ok:
		checks["auth"] = "pass"
	default:
		checks["auth"] = "fail"
	}
	_ = user

	if a.DB != nil {
		if _, err := a.DB.JournalMode(ctx); err != nil {
			checks["db"] = "fail"
		} else {
			checks["db"] = "pass"
		}
	} else {
		checks["db"] = "unknown"
	}

	channelID, _, chErr := a.channelID(ctx)
	if chErr != nil {
		checks["channel"] = "fail"
		checks["upload"] = "unknown"
		checks["delete"] = "unknown"
		checks["invite_link"] = "unknown"
		checks["edit_old_caption"] = "unknown"
		checks["file_size_limit"] = "unknown"
	} else {
		checks["channel"] = "pass"
		tgChID, _ := a.tgChannelID(ctx)
		caps, err := a.TG.Doctor(ctx, tgChID)
		if err != nil {
			checks["upload"] = "unknown"
		} else {
			checks["upload"] = boolCheck(caps.UploadOK)
			checks["delete"] = boolCheck(caps.DeleteOK)
			checks["invite_link"] = boolCheck(caps.InviteLinkOK)
			checks["edit_old_caption"] = boolCheck(caps.EditOldCaptionOK)
			switch {
			case caps.MaxUploadBytes >= a.Cfg.Limits.PremiumUploadBytes:
				checks["file_size_limit"] = "pass"
			case caps.MaxUploadBytes >= a.Cfg.Limits.FreeUploadBytes:
				checks["file_size_limit"] = "warn"
			default:
				checks["file_size_limit"] = "fail"
			}
			_, _ = a.DB.Raw().ExecContext(ctx, `update channels set updated_at=? where id=?`, time.Now().UTC().Format(time.RFC3339), channelID)
		}
	}
	return map[string]any{"checks": checks}, nil
}

func boolCheck(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

// Ensure imports used
var _ = sql.ErrNoRows
