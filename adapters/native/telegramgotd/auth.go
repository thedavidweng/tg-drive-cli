package telegramgotd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-faster/errors"
	tdtelegram "github.com/gotd/td/telegram"
	tdauth "github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/core/telegram"
)

const (
	// codeEntryAttempts bounds re-prompts for a mistyped code before giving
	// up. Re-prompting does not resend the code.
	codeEntryAttempts = 3
	// passwordAttempts bounds 2FA password re-prompts.
	passwordAttempts = 3
	// loginStateTTL bounds how long a sent code is considered reusable across
	// login invocations. A stale hash surfaces as PHONE_CODE_EXPIRED and
	// triggers a single automatic resend.
	loginStateTTL = 10 * time.Minute
)

// Login flow stages persisted in loginState.
const (
	stageCode     = ""         // waiting for the login code
	stagePassword = "password" // code accepted, waiting for the 2FA password
)

// loginState persists the pending SendCode exchange so a re-run of
// `td auth login` reuses the already-sent code instead of invalidating it
// with a fresh SendCode call. Repeated SendCode calls are what trigger
// Telegram to flood-wait the account. Stage records how far the flow got so
// a re-run resumes at the right prompt.
type loginState struct {
	Phone         string    `json:"phone"`
	PhoneCodeHash string    `json:"phone_code_hash"`
	SentAt        time.Time `json:"sent_at"`
	Stage         string    `json:"stage,omitempty"`
}

func loginStatePath(sessionPath string) string {
	return filepath.Join(filepath.Dir(sessionPath), "login_state.json")
}

// loadLoginState returns the persisted pending-code state when it matches
// phone and has not aged past loginStateTTL.
func loadLoginState(sessionPath, phone string, now time.Time) (loginState, bool) {
	data, err := os.ReadFile(loginStatePath(sessionPath))
	if err != nil {
		return loginState{}, false
	}
	var st loginState
	if err := json.Unmarshal(data, &st); err != nil {
		return loginState{}, false
	}
	if st.Phone != phone || st.PhoneCodeHash == "" || now.Sub(st.SentAt) > loginStateTTL {
		return loginState{}, false
	}
	return st, true
}

// saveLoginState persists st best-effort: failure to save only means the next
// login run sends a fresh code.
func saveLoginState(sessionPath string, st loginState) {
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(loginStatePath(sessionPath)), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(loginStatePath(sessionPath), data, 0o600)
}

func clearLoginState(sessionPath string) {
	_ = os.Remove(loginStatePath(sessionPath))
}

func (c *Client) Login(ctx context.Context, apiID int64, apiHash, phone string, codeFn tgtelegram.CodeFunc, passwordFn tgtelegram.PasswordFunc, opts tgtelegram.LoginOptions) (*tgtelegram.LoginResult, error) {
	if apiID != 0 {
		c.apiID = int(apiID)
	}
	if apiHash != "" {
		c.apiHash = apiHash
	}
	var out *tgtelegram.LoginResult
	err := c.run(ctx, func(ctx context.Context, _ *tg.Client, client *tdtelegram.Client) error {
		already := false
		if status, err := client.Auth().Status(ctx); err == nil && status.Authorized {
			already = true
		} else if err := c.signInFlow(ctx, client.Auth(), phone, codeFn, passwordFn, opts); err != nil {
			return err
		}
		clearLoginState(c.sessionPath)
		self, err := client.Self(ctx)
		if err != nil {
			return mapRPCError(err)
		}
		out = &tgtelegram.LoginResult{
			User: tgtelegram.User{
				ID:          self.ID,
				Phone:       phoneNumber(self),
				DisplayName: userDisplay(self),
			},
			AlreadyAuthorized: already,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// signInFlow drives SendCode/SignIn manually (instead of tdauth.Flow) so a
// previously sent code is reused across invocations and a mistyped code
// re-prompts without sending a new one.
func (c *Client) signInFlow(ctx context.Context, a *tdauth.Client, phone string, codeFn tgtelegram.CodeFunc, passwordFn tgtelegram.PasswordFunc, opts tgtelegram.LoginOptions) error {
	st, reused := loadLoginState(c.sessionPath, phone, time.Now().UTC())
	if reused && st.Stage == stagePassword && !opts.ForceNewCode {
		// A previous run already had its code accepted and stopped at the 2FA
		// password. The password-pending state lives on the persisted session,
		// so resume there instead of re-asking for a code.
		err := passwordFlow(ctx, a, passwordFn)
		if err == nil {
			return nil
		}
		var authErr *tgtelegram.AuthRequiredError
		if !errors.As(err, &authErr) {
			return err
		}
		// The pending-password state is gone server-side: restart the flow.
		clearLoginState(c.sessionPath)
		reused = false
	}
	resent := false
	if opts.ForceNewCode || !reused {
		var err error
		st, err = c.sendCode(ctx, a, phone)
		if err != nil {
			return err
		}
		if st.PhoneCodeHash == "" {
			// AuthSentCodeSuccess: authorized without a code exchange.
			return nil
		}
		reused = false
	}
	for attempt := 1; attempt <= codeEntryAttempts; attempt++ {
		code, err := codeFn(tgtelegram.CodePrompt{
			Attempt:     attempt,
			MaxAttempts: codeEntryAttempts,
			Reused:      reused,
			Resent:      resent && attempt == 1,
			SentAt:      st.SentAt,
		})
		if err != nil {
			return err
		}
		_, err = a.SignIn(ctx, phone, strings.TrimSpace(code), st.PhoneCodeHash)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, tdauth.ErrPasswordAuthNeeded):
			// The code is consumed; remember that only the password is left so
			// a re-run resumes at the password prompt.
			st.Stage = stagePassword
			saveLoginState(c.sessionPath, st)
			return passwordFlow(ctx, a, passwordFn)
		case tgerr.Is(err, "PHONE_CODE_INVALID"):
			continue
		case tgerr.Is(err, "PHONE_CODE_EXPIRED", "AUTH_RESTART"):
			// The reused (or aged) code is dead server-side. Send exactly one
			// fresh code and restart the entry attempts.
			if resent {
				if tgerr.Is(err, "PHONE_CODE_EXPIRED") {
					return &tgtelegram.CodeExpiredError{}
				}
				return mapRPCError(err)
			}
			resent = true
			clearLoginState(c.sessionPath)
			st, err = c.sendCode(ctx, a, phone)
			if err != nil {
				return err
			}
			if st.PhoneCodeHash == "" {
				return nil
			}
			reused = false
			attempt = 0
		default:
			return mapRPCError(err)
		}
	}
	return &tgtelegram.CodeInvalidError{Attempts: codeEntryAttempts}
}

// sendCode requests a fresh login code and persists the exchange state. An
// empty PhoneCodeHash in the returned state means Telegram authorized the
// session without a code (AuthSentCodeSuccess).
func (c *Client) sendCode(ctx context.Context, a *tdauth.Client, phone string) (loginState, error) {
	sent, err := a.SendCode(ctx, phone, tdauth.SendCodeOptions{})
	if err != nil {
		if tgerr.Is(err, "PHONE_NUMBER_INVALID", "PHONE_NUMBER_BANNED") {
			return loginState{}, &tgtelegram.PhoneInvalidError{}
		}
		return loginState{}, mapRPCError(err)
	}
	switch code := sent.(type) {
	case *tg.AuthSentCode:
		st := loginState{Phone: phone, PhoneCodeHash: code.PhoneCodeHash, SentAt: time.Now().UTC()}
		saveLoginState(c.sessionPath, st)
		return st, nil
	case *tg.AuthSentCodeSuccess:
		if _, ok := code.Authorization.(*tg.AuthAuthorization); ok {
			return loginState{}, nil
		}
		return loginState{}, errors.New("this phone number has no Telegram account; sign up in a Telegram app first")
	case *tg.AuthSentCodePaymentRequired:
		return loginState{}, errors.New("telegram requires payment to log in with this number; anonymous/Fragment numbers are not supported")
	default:
		return loginState{}, errors.Errorf("unexpected sent code type %T", sent)
	}
}

func passwordFlow(ctx context.Context, a *tdauth.Client, passwordFn tgtelegram.PasswordFunc) error {
	if passwordFn == nil {
		return mapRPCError(tdauth.ErrPasswordNotProvided)
	}
	for attempt := 1; attempt <= passwordAttempts; attempt++ {
		pw, err := passwordFn()
		if err != nil {
			return err
		}
		_, err = a.Password(ctx, strings.TrimSpace(pw))
		switch {
		case err == nil:
			return nil
		case errors.Is(err, tdauth.ErrPasswordInvalid):
			if attempt == passwordAttempts {
				return &tgtelegram.PasswordInvalidError{}
			}
		default:
			return mapRPCError(err)
		}
	}
	return &tgtelegram.PasswordInvalidError{}
}

func (c *Client) Status(ctx context.Context) (*tgtelegram.User, bool, error) {
	var out *tgtelegram.User
	err := c.run(ctx, func(ctx context.Context, _ *tg.Client, client *tdtelegram.Client) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return mapRPCError(err)
		}
		if !status.Authorized {
			return nil
		}
		self, err := client.Self(ctx)
		if err != nil {
			return mapRPCError(err)
		}
		out = &tgtelegram.User{
			ID:          self.ID,
			Phone:       phoneNumber(self),
			DisplayName: userDisplay(self),
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if out == nil {
		return nil, false, nil
	}
	return out, true, nil
}

func (c *Client) Logout(ctx context.Context) error {
	return c.run(ctx, func(ctx context.Context, api *tg.Client, _ *tdtelegram.Client) error {
		if _, err := api.AuthLogOut(ctx); err != nil {
			return mapRPCError(err)
		}
		_ = os.Remove(c.sessionPath)
		clearLoginState(c.sessionPath)
		return nil
	})
}
