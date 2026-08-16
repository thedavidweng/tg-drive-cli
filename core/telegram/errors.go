package telegram

import (
	"errors"
	"fmt"
	"time"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
)

// MapError classifies a Telegram-layer error into the stable application
// error taxonomy. It is the single mapper shared by the service and publisher
// layers; it unwraps so wrapped adapter errors classify the same as bare ones.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if ae, ok := apperr.As(err); ok {
		return ae
	}

	var fw *FloodWaitError
	if errors.As(err, &fw) {
		wait := time.Duration(fw.Seconds) * time.Second
		retryAt := time.Now().Add(wait)
		return apperr.New(apperr.ErrTelegramRateLimited,
			fmt.Sprintf("telegram rate limited this account: retry after %s (at %s)",
				wait, retryAt.Format("2006-01-02 15:04 MST"))).
			WithDetails(map[string]any{
				"retry_after_seconds": fw.Seconds,
				"retry_at":            retryAt.UTC().Format(time.RFC3339),
			})
	}

	var auth *AuthRequiredError
	var code *CodeInvalidError
	var pass *PasswordInvalidError
	var expired *CodeExpiredError
	var phone *PhoneInvalidError
	var large *FileTooLargeError
	var perm *PermissionDeniedError
	var notEditable *MessageNotEditableError
	var notFound *MessageNotFoundError

	switch {
	case errors.As(err, &auth):
		return apperr.New(apperr.ErrAuthRequired, "not logged in; run: td auth login")
	case errors.As(err, &code), errors.As(err, &pass), errors.As(err, &expired):
		return apperr.New(apperr.ErrAuthFailed, err.Error())
	case errors.As(err, &phone):
		return apperr.New(apperr.ErrConfigInvalid, err.Error())
	case errors.As(err, &large):
		return apperr.New(apperr.ErrFileTooLarge, err.Error())
	case errors.As(err, &perm):
		return apperr.New(apperr.ErrChannelPermission, err.Error())
	case errors.As(err, &notEditable):
		return apperr.New(apperr.ErrMessageNotEditable, err.Error())
	case errors.As(err, &notFound):
		return apperr.New(apperr.ErrRemoteNotFound, err.Error())
	}
	return apperr.Wrap(apperr.ErrTelegramRPC, "telegram", err)
}
