package service

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/pathcodec"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
)

// AuthLogin performs interactive login.
func (a *App) AuthLogin(ctx context.Context, codeFn telegram.CodeFunc, passwordFn telegram.PasswordFunc, opts telegram.LoginOptions) (map[string]any, error) {
	if a.Cfg.Telegram.APIID == 0 || a.Cfg.Telegram.APIHash == "" {
		return nil, apperr.New(apperr.ErrConfigMissing, "telegram.api_id and telegram.api_hash required")
	}
	phone := a.Cfg.Telegram.Phone
	if phone == "" {
		return nil, apperr.New(apperr.ErrConfigMissing, "telegram.phone required")
	}
	res, err := a.TG.Login(ctx, a.Cfg.Telegram.APIID, a.Cfg.Telegram.APIHash, phone, codeFn, passwordFn, opts)
	if err != nil {
		return nil, mapTGErr(err)
	}
	user := res.User
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = a.DB.Raw().ExecContext(ctx, `
		insert into accounts(tg_user_id,phone,display_name,created_at,updated_at) values(?,?,?,?,?)
		on conflict(tg_user_id) do update set phone=excluded.phone, display_name=excluded.display_name, updated_at=excluded.updated_at`,
		fmt.Sprintf("%d", user.ID), user.Phone, user.DisplayName, now, now)
	return map[string]any{
		"user_id":               user.ID,
		"display_name":          user.DisplayName,
		"phone":                 config.RedactValue("telegram.phone", user.Phone, false),
		"already_authenticated": res.AlreadyAuthorized,
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
		return nil, apperr.New(apperr.ErrAuthRequired, "not logged in; run: td auth login")
	}
	// Re-running init on an already-bound root must not mint another channel:
	// channel creation is expensive on a real account and cannot be undone
	// from here.
	if create != "" {
		if existing := a.findRootBinding(ctx, user.ID, localRoot); existing != nil {
			return map[string]any{
				"channel_id":          existing.ID,
				"channel_title":       existing.Title,
				"local_root":          localRoot,
				"already_initialized": true,
			}, nil
		}
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

// findRootBinding returns the channel already bound to localRoot for the
// given account, comparing absolute paths.
func (a *App) findRootBinding(ctx context.Context, tgUserID int64, localRoot string) *telegram.Channel {
	absRoot, err := filepath.Abs(localRoot)
	if err != nil {
		return nil
	}
	rows, err := a.DB.Raw().QueryContext(ctx, `
		select c.tg_channel_id, c.title, c.root_local_path
		from channels c join accounts a on a.id = c.account_id
		where a.tg_user_id = ?`, fmt.Sprintf("%d", tgUserID))
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var idStr, title, root string
		if rows.Scan(&idStr, &title, &root) != nil {
			continue
		}
		absStored, err := filepath.Abs(root)
		if err != nil || absStored != absRoot {
			continue
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		return &telegram.Channel{ID: id, Title: title}
	}
	return nil
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
		// Share targets a directory subtree: include a synthetic leaf so the
		// chain covers the full shared path, then take the deepest tag.
		chainPath := p
		if chainPath != "/" {
			chainPath += "/_"
		}
		tags, _, _ := pathcodec.GenerateChain(chainPath, existing)
		if len(tags) > 0 {
			tag = tags[len(tags)-1]
		}
	}
	var title string
	_ = a.DB.Raw().QueryRowContext(ctx, `select title from channels where id=?`, channelID).Scan(&title)
	return map[string]any{
		"path":        p,
		"channel":     title,
		"invite_link": link,
		"hashtag":     tag,
	}, nil
}

// Doctor runs capability checks.
func (a *App) Doctor(ctx context.Context) (map[string]any, error) {
	checks := map[string]string{}
	hints := map[string]string{}
	out := map[string]any{}

	if a.Cfg.Telegram.APIID != 0 && a.Cfg.Telegram.APIHash != "" {
		checks["config"] = "pass"
	} else {
		checks["config"] = "warn"
		hints["config"] = "telegram.api_id / api_hash not set; run: td auth setup"
	}
	if _, err := os.Stat(a.Cfg.Storage.SessionPath); err == nil {
		checks["session_file"] = "pass"
	} else {
		checks["session_file"] = "warn"
		hints["session_file"] = fmt.Sprintf("no session file at %s; run: td auth login", a.Cfg.Storage.SessionPath)
	}
	checks["caption_counter"] = captionCounterSelfTest()
	checks["path_codec"] = pathCodecSelfTest()

	user, ok, err := a.TG.Status(ctx)
	switch {
	case err != nil:
		checks["auth"] = "fail"
		hints["auth"] = "could not reach Telegram; check network, then run: td auth login"
	case ok:
		checks["auth"] = "pass"
	default:
		checks["auth"] = "fail"
		hints["auth"] = "not logged in; run: td auth login"
	}
	_ = user

	if a.DB != nil {
		if _, err := a.DB.JournalMode(ctx); err != nil {
			checks["db"] = "fail"
			hints["db"] = fmt.Sprintf("database error at %s; check storage.db_path", a.Cfg.Storage.DBPath)
		} else {
			checks["db"] = "pass"
		}
	} else {
		checks["db"] = "unknown"
	}

	channelID, _, chErr := a.channelID(ctx)
	if chErr != nil {
		checks["channel"] = "fail"
		hints["channel"] = "no channel bound; run: td init <local-root> --create-channel"
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
			out["max_upload_bytes"] = caps.MaxUploadBytes
			switch {
			case caps.MaxUploadBytes >= a.Cfg.Limits.PremiumUploadBytes:
				checks["file_size_limit"] = "pass"
			case caps.MaxUploadBytes >= a.Cfg.Limits.FreeUploadBytes:
				checks["file_size_limit"] = "warn"
				hints["file_size_limit"] = fmt.Sprintf("free-tier upload limit (%d bytes); Telegram Premium raises it", caps.MaxUploadBytes)
			default:
				checks["file_size_limit"] = "fail"
				hints["file_size_limit"] = "upload limit below the expected free tier; check account status"
			}
			_, _ = a.DB.Raw().ExecContext(ctx, `update channels set updated_at=? where id=?`, time.Now().UTC().Format(time.RFC3339), channelID)
		}
	}
	out["checks"] = checks
	if len(hints) > 0 {
		out["hints"] = hints
	}
	return out, nil
}

func boolCheck(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

// captionCounterSelfTest verifies UTF-16 code unit counting on fixed vectors.
func captionCounterSelfTest() string {
	vectors := []struct {
		s    string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"中文", 2},
		{"📷", 2},
		{"a📷b", 4},
	}
	for _, v := range vectors {
		if manifest.UTF16Units(v.s) != v.want {
			return "fail"
		}
	}
	return "pass"
}

// pathCodecSelfTest verifies slug generation stays inside the Telegram-safe
// hashtag charset on fixed vectors.
func pathCodecSelfTest() string {
	for _, p := range []string{"/My Photos/2024/x.jpg", "/图片/2024/x.jpg", "/emoji/📷/x.jpg", "/a_b/c/x.txt"} {
		tags, _, err := pathcodec.GenerateChain(p, map[string]string{})
		if err != nil {
			return "fail"
		}
		for _, tag := range tags {
			if !strings.HasPrefix(tag, "#td_") {
				return "fail"
			}
			for _, r := range tag[1:] {
				safe := r == '_' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
				if !safe {
					return "fail"
				}
			}
		}
	}
	return "pass"
}

// Ensure imports used
var _ = sql.ErrNoRows
