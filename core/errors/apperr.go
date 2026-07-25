package apperr

import (
	"errors"
	"fmt"
)

// Error codes from docs/contracts/cli-contract.md.
const (
	ErrUsage                      = "ERR_USAGE"
	ErrFlagConflict               = "ERR_FLAG_CONFLICT"
	ErrAuthRequired               = "ERR_AUTH_REQUIRED"
	ErrAuthFailed                 = "ERR_AUTH_FAILED"
	ErrConfigMissing              = "ERR_CONFIG_MISSING"
	ErrConfigInvalid              = "ERR_CONFIG_INVALID"
	ErrChannelNotFound            = "ERR_CHANNEL_NOT_FOUND"
	ErrChannelPermission          = "ERR_CHANNEL_PERMISSION"
	ErrPathInvalid                = "ERR_PATH_INVALID"
	ErrPathExists                 = "ERR_PATH_EXISTS"
	ErrPathIsDirectory            = "ERR_PATH_IS_DIRECTORY"
	ErrPathAncestorIsFile         = "ERR_PATH_ANCESTOR_IS_FILE"
	ErrPathConflict               = "ERR_PATH_CONFLICT"
	ErrEmptyDirsUnsupported       = "ERR_EMPTY_DIRS_UNSUPPORTED"
	ErrOrphanedUpload             = "ERR_ORPHANED_UPLOAD"
	ErrLocalPathExists            = "ERR_LOCAL_PATH_EXISTS"
	ErrLocalNotFound              = "ERR_LOCAL_NOT_FOUND"
	ErrRemoteNotFound             = "ERR_REMOTE_NOT_FOUND"
	ErrFileTooLarge               = "ERR_FILE_TOO_LARGE"
	ErrCaptionTooLong             = "ERR_CAPTION_TOO_LONG"
	ErrManifestInvalid            = "ERR_MANIFEST_INVALID"
	ErrCrossChannelMove           = "ERR_CROSS_CHANNEL_MOVE"
	ErrDirectoryMoveUnsupported   = "ERR_DIRECTORY_MOVE_UNSUPPORTED"
	ErrDirectoryDeleteUnsupported = "ERR_DIRECTORY_DELETE_UNSUPPORTED"
	ErrMessageNotEditable         = "ERR_MESSAGE_NOT_EDITABLE"
	ErrScanFailed                 = "ERR_SCAN_FAILED"
	ErrTelegramRateLimited        = "ERR_TELEGRAM_RATE_LIMITED"
	ErrTelegramRPC                = "ERR_TELEGRAM_RPC"
	ErrDB                         = "ERR_DB"
	ErrOperationLocked            = "ERR_OPERATION_LOCKED"
	ErrRepairRequired             = "ERR_REPAIR_REQUIRED"
	ErrSlugCollision              = "ERR_SLUG_COLLISION"
)

// AppError is a structured application error with stable code and exit mapping.
type AppError struct {
	Code    string
	Message string
	Details map[string]any
}

func (e *AppError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// New creates a new AppError.
func New(code, message string) *AppError {
	return &AppError{Code: code, Message: message, Details: map[string]any{}}
}

// WithDetails returns a copy with details attached.
func (e *AppError) WithDetails(details map[string]any) *AppError {
	cp := *e
	if details != nil {
		cp.Details = details
	}
	return &cp
}

// ExitCode maps error codes to process exit codes per cli-contract.
func ExitCode(err error) int {
	var ae *AppError
	if !errors.As(err, &ae) {
		return 1
	}
	switch ae.Code {
	case ErrUsage, ErrFlagConflict, ErrPathInvalid, ErrPathExists,
		ErrPathIsDirectory, ErrPathAncestorIsFile, ErrPathConflict,
		ErrLocalPathExists, ErrLocalNotFound, ErrRemoteNotFound,
		ErrDirectoryMoveUnsupported, ErrDirectoryDeleteUnsupported,
		ErrCrossChannelMove, ErrEmptyDirsUnsupported, ErrSlugCollision:
		return 2
	case ErrAuthRequired, ErrAuthFailed, ErrConfigMissing, ErrConfigInvalid:
		return 3
	case ErrChannelNotFound, ErrChannelPermission, ErrFileTooLarge,
		ErrMessageNotEditable, ErrTelegramRateLimited, ErrTelegramRPC:
		return 4
	case ErrDB, ErrScanFailed, ErrManifestInvalid, ErrOperationLocked,
		ErrRepairRequired, ErrCaptionTooLong, ErrOrphanedUpload:
		return 5
	default:
		return 1
	}
}

// As extracts an AppError from err.
func As(err error) (*AppError, bool) {
	var ae *AppError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}

// Wrap wraps err with code and message.
func Wrap(code, message string, err error) *AppError {
	if err == nil {
		return New(code, message)
	}
	ae := New(code, fmt.Sprintf("%s: %v", message, err))
	return ae
}
