package telegram

import (
	"context"
	"io"
	"time"
)

// User represents an authenticated Telegram user.
type User struct {
	ID          int64
	Phone       string
	DisplayName string
}

// Channel represents a Telegram channel.
type Channel struct {
	ID         int64
	AccessHash int64
	Title      string
	Username   string
	InviteLink string
}

// Message represents a channel message with optional media.
type Message struct {
	ID          int
	Text        string
	Caption     string
	FileName    string
	FileSize    int64
	MIME        string
	Data        []byte
	ReplyTo     *int
	NotEditable bool
}

// UploadRequest is a media upload request.
type UploadRequest struct {
	ChannelID int64
	Caption   string
	FileName  string
	MIME      string
	Size      int64
	Reader    io.Reader
}

// UploadResult is returned after upload.
type UploadResult struct {
	MessageID int
}

// Capabilities describes runtime Telegram capabilities.
type Capabilities struct {
	AuthOK           bool
	ChannelOK        bool
	UploadOK         bool
	DeleteOK         bool
	InviteLinkOK     bool
	EditOldCaptionOK bool
	MaxUploadBytes   int64
	CheckedAt        time.Time
}

// Client is the aggregate Telegram client interface.
type Client interface {
	AuthClient
	ChannelClient
	MediaClient
	HistoryClient
}

// AuthClient handles authentication.
type AuthClient interface {
	Login(ctx context.Context, apiID int64, apiHash, phone string, codeFn, passwordFn func() (string, error)) (*User, error)
	Status(ctx context.Context) (*User, bool, error)
	Logout(ctx context.Context) error
}

// ChannelClient handles channel operations.
type ChannelClient interface {
	CreateChannel(ctx context.Context, title string) (*Channel, error)
	ResolveChannel(ctx context.Context, titleOrID string) (*Channel, error)
	BindChannel(ctx context.Context, titleOrID string) (*Channel, error)
	GetInviteLink(ctx context.Context, channelID int64) (string, error)
}

// MediaClient handles media operations.
type MediaClient interface {
	UploadMedia(ctx context.Context, req UploadRequest) (*UploadResult, error)
	SendTextReply(ctx context.Context, channelID int64, replyTo int, text string) (int, error)
	EditCaption(ctx context.Context, channelID int64, messageID int, caption string) error
	EditText(ctx context.Context, channelID int64, messageID int, text string) error
	DeleteMessage(ctx context.Context, channelID int64, messageID int) error
	DownloadMedia(ctx context.Context, channelID int64, messageID int, dst io.Writer) error
	Doctor(ctx context.Context, channelID int64) (*Capabilities, error)
}

// HistoryClient fetches message history.
type HistoryClient interface {
	History(ctx context.Context, channelID int64, afterID int, limit int) ([]Message, error)
}

// Typed errors.
type FloodWaitError struct {
	Seconds int
}

func (e *FloodWaitError) Error() string { return "flood wait" }

type PermissionDeniedError struct{}

func (e *PermissionDeniedError) Error() string { return "permission denied" }

type MessageNotEditableError struct{}

func (e *MessageNotEditableError) Error() string { return "message not editable" }

type MessageNotFoundError struct{}

func (e *MessageNotFoundError) Error() string { return "message not found" }

type FileTooLargeError struct{}

func (e *FileTooLargeError) Error() string { return "file too large" }

type AuthRequiredError struct{}

func (e *AuthRequiredError) Error() string { return "auth required" }
