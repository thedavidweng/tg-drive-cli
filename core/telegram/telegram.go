package telegram

import (
	"context"
	"fmt"
	"io"
	"time"
)

// User represents an authenticated Telegram user.
type User struct {
	ID          int64
	Phone       string
	DisplayName string
}

// CodePrompt tells the login code callback why it is being invoked, so the
// caller can render an accurate prompt.
type CodePrompt struct {
	// Attempt is the 1-based entry attempt for the current code. Attempt > 1
	// means the previous entry was rejected as invalid.
	Attempt int
	// MaxAttempts is how many entry attempts are allowed for this code.
	MaxAttempts int
	// Reused is true when the prompt is for a code sent by an earlier login
	// invocation (no new code was sent this run).
	Reused bool
	// Resent is true when a fresh code was just sent because the previous
	// one expired.
	Resent bool
	// SentAt is when the code was sent, if known.
	SentAt time.Time
}

// CodeFunc supplies the login code sent by Telegram.
type CodeFunc func(prompt CodePrompt) (string, error)

// PasswordFunc supplies the 2FA password.
type PasswordFunc func() (string, error)

// LoginOptions controls the interactive login flow.
type LoginOptions struct {
	// ForceNewCode requests a fresh code even when a previously sent code is
	// still reusable.
	ForceNewCode bool
}

// LoginResult is returned after a successful login.
type LoginResult struct {
	User User
	// AlreadyAuthorized is true when the session was already authenticated
	// and no code exchange happened.
	AlreadyAuthorized bool
}

// Channel represents a Telegram channel.
type Channel struct {
	ID         int64
	AccessHash int64
	Title      string
	Username   string
	InviteLink string
}

// ListChannelsOptions filters channel listing.
type ListChannelsOptions struct {
	// OnlyDrive filters to channels whose title contains the [TD] suffix.
	OnlyDrive bool
}

// UploadProgressState reports upload progress.
type UploadProgressState struct {
	FileName string
	Part     int
	PartSize int
	Uploaded int64
	Total    int64
}

// UploadProgress is called as each part is confirmed.
type UploadProgress func(ctx context.Context, state UploadProgressState) error

// ResumableStore persists part-level upload state for crash/resume recovery.
type ResumableStore interface {
	LoadUploadState(ctx context.Context, key string) (*UploadState, error)
	SaveUploadState(ctx context.Context, key string, state *UploadState) error
	DeleteUploadState(ctx context.Context, key string) error
}

// UploadState is persisted resumable state.
type UploadState struct {
	FileID         int64
	PartSize       int
	TotalParts     int
	TotalBytes     int64
	ContentHash    string
	ConfirmedParts []int
	ConfirmedBytes int64
}

// ResumableBigFileBytes is the size above which uploads use the resumable
// saveBigFilePart path (part state persisted, retries send only unconfirmed
// parts). The service and adapters share this cutoff.
const ResumableBigFileBytes = 10 * 1024 * 1024

// Media kinds Telegram can store as a drive file.
const (
	KindNone     = ""
	KindDocument = "document"
	KindPhoto    = "photo"
	KindText     = "text"
	// KindVideo requests a document carrying a video attribute block: bytes
	// are stored untouched (it is still a document) but native clients render
	// it as a playable, streamable video card.
	KindVideo = "video"
)

// VideoAttributes is the video attribute block of a video-kind upload or
// message. Callers supply the values; td never probes media files itself.
type VideoAttributes struct {
	// DurationSeconds is the video length in seconds.
	DurationSeconds float64
	// Width and Height are the pixel dimensions.
	Width  int
	Height int
	// SupportsStreaming marks the video as streamable in native clients.
	SupportsStreaming bool
}

// ForwardOrigin is the forward-header snapshot of a message: where its bytes
// were first published. The title is a snapshot on purpose — the origin
// channel can vanish, and its name is then the only human trace left.
type ForwardOrigin struct {
	// FromID is the origin channel or user id; 0 when Telegram hid it.
	FromID int64
	// Title is the origin channel title or sender name as it read at the
	// time of the read.
	Title string
	// PostID is the origin channel post id; 0 for non-channel origins.
	PostID int
	// Date is when the origin message was published; zero when unknown.
	Date time.Time
}

// Message represents a channel message with optional media.
type Message struct {
	ID          int
	Text        string
	Caption     string
	FileName    string
	FileSize    int64
	MIME        string
	Kind        string
	Data        []byte
	ReplyTo     *int
	NotEditable bool
	// Date is when the message was posted; zero when unknown.
	Date time.Time
	// Forward is the forward-header snapshot of a forwarded message; nil
	// when the message was posted directly.
	Forward *ForwardOrigin
	// SavedPeerID names the Saved Messages 2.0 sub-chat a saved message
	// lives in; 0 means the main saved chat. Only saved-chat reads set it.
	SavedPeerID int64
	// SavedPeerTitle is that sub-chat's display name as resolved at read
	// time; empty when the adapter could not resolve one.
	SavedPeerTitle string
	// GroupedID is Telegram's media-album id. 0 means the message is not
	// part of an album. Members of one album share a single human caption
	// on the first item and appear as one timeline block.
	GroupedID int64
	// Video carries the video attribute block of a video-kind document;
	// nil means the message has none.
	Video *VideoAttributes
	// Thumb holds JPEG thumbnail bytes attached at upload time; nil means
	// the message has none. History reads cannot recover thumbnail bytes,
	// so only uploads and the fake adapter populate this.
	Thumb []byte
}

// UploadRequest is a media upload request.
type UploadRequest struct {
	ChannelID   int64
	Caption     string
	FileName    string
	MIME        string
	Size        int64
	ContentHash string
	Reader      io.Reader
	// Kind selects how native clients present the upload: KindNone or
	// KindDocument sends a plain file card (the historical behavior),
	// KindPhoto a native photo message, KindVideo a document with video
	// attributes.
	Kind string
	// Video carries the attribute block for KindVideo uploads. Adapters send
	// a (possibly zero-valued) video attribute block whenever Kind is
	// KindVideo, filling it from this field when present.
	Video *VideoAttributes
	// Thumb is an optional JPEG thumbnail body attached to document sends so
	// previews appear immediately, before Telegram generates its own.
	Thumb []byte
	// Path is the local file path, used by resumable big-file uploads.
	Path string
	// Threads is the number of parallel upload goroutines. <=1 uses defaults.
	Threads int
	// PartSize sets the part size. <=0 uses defaults.
	PartSize int
	// Progress reports confirmed upload parts.
	Progress UploadProgress
	// ResumableKey identifies this upload in ResumableStore. Empty disables.
	ResumableKey string
	// ResumableStore persists part state for big-file resumption.
	ResumableStore ResumableStore
}

// UploadResult is returned after upload.
type UploadResult struct {
	MessageID int
	// GroupedID is Telegram's media-album id for members sent through
	// UploadMediaGroup; 0 for single uploads.
	GroupedID int64
}

// MaxMediaGroupMembers is Telegram's per-group member limit for
// UploadMediaGroup.
const MaxMediaGroupMembers = 10

// Capabilities describes runtime Telegram capabilities.
type Capabilities struct {
	AuthOK           bool
	ChannelOK        bool
	UploadOK         bool
	DeleteOK         bool
	InviteLinkOK     bool
	EditOldCaptionOK bool
	// DiscussionOK reports that the channel has a linked discussion group,
	// the carrier of machine manifest comments (ADR 0018).
	DiscussionOK bool
	// SavedHistoryOK reports that the Saved Messages chat's history is
	// readable, and SavedDeleteOK that saved messages can be deleted — the
	// capabilities td import saved needs.
	SavedHistoryOK bool
	SavedDeleteOK  bool
	MaxUploadBytes int64
	CheckedAt      time.Time
}

// Client is the aggregate Telegram client interface.
type Client interface {
	AuthClient
	ChannelClient
	MediaClient
	HistoryClient
	DiscussionClient
	SavedClient
}

// AuthClient handles authentication.
type AuthClient interface {
	Login(ctx context.Context, apiID int64, apiHash, phone string, codeFn CodeFunc, passwordFn PasswordFunc, opts LoginOptions) (*LoginResult, error)
	Status(ctx context.Context) (*User, bool, error)
	Logout(ctx context.Context) error
}

// ChannelClient handles channel operations.
type ChannelClient interface {
	CreateChannel(ctx context.Context, title string) (*Channel, error)
	ResolveChannel(ctx context.Context, titleOrID string) (*Channel, error)
	GetInviteLink(ctx context.Context, channelID int64) (string, error)
	ListChannels(ctx context.Context, opts ListChannelsOptions) ([]Channel, error)
}

// MediaClient handles media operations.
type MediaClient interface {
	UploadMedia(ctx context.Context, req UploadRequest) (*UploadResult, error)
	// UploadMediaGroup sends 1..MaxMediaGroupMembers requests as one native
	// media group (sendMultiMedia). All requests must target the same
	// channel and carry the same Kind; only the first request's Caption is
	// honored — sibling captions stay empty, matching the album conventions
	// of ADR 0013. It returns one result per request, in request order, all
	// sharing a single non-zero GroupedID.
	UploadMediaGroup(ctx context.Context, reqs []UploadRequest) ([]UploadResult, error)
	SendTextReply(ctx context.Context, channelID int64, replyTo int, text string) (int, error)
	EditCaption(ctx context.Context, channelID int64, messageID int, caption string) error
	EditText(ctx context.Context, channelID int64, messageID int, text string) error
	DeleteMessage(ctx context.Context, channelID int64, messageID int) error
	// DownloadMedia streams the media body of a message into dst.
	DownloadMedia(ctx context.Context, channelID int64, messageID int, dst io.Writer) error
	Doctor(ctx context.Context, channelID int64) (*Capabilities, error)
}

// HistoryMeta reports what a history read provably covered.
type HistoryMeta struct {
	// Complete is true only when pagination provably reached the channel's
	// oldest message (or the afterID boundary). A read that merely stopped
	// early — a short page that never crossed the boundary and never hit the
	// reported total — sets Complete to false so callers can refuse to act on
	// a partial view.
	Complete bool
	// TotalMessages is the channel's reported total message count, when known.
	TotalMessages int
	// OldestID is the lowest message id the read covered, 0 when none.
	OldestID int
}

// HistoryClient fetches message history.
//
// Ordering contract: history reads return messages newest-first (descending
// message id), matching Telegram's messages.getHistory. Implementations must
// preserve that order page by page; scans rely on it for newest-message-wins
// reconciliation.
type HistoryClient interface {
	// History returns messages newer than afterID, newest-first.
	History(ctx context.Context, channelID int64, afterID, limit int) ([]Message, error)
	// StreamHistory feeds each message newer than afterID to fn, newest-first,
	// without materializing the whole channel. Returning an error from fn stops
	// the stream. The HistoryMeta describes whether the read provably covered
	// everything down to the afterID boundary.
	StreamHistory(ctx context.Context, channelID int64, afterID int, fn func(Message) error) (HistoryMeta, error)
	GetMessage(ctx context.Context, channelID int64, messageID int) (Message, error)
}

// SavedClient reads and mutates the authenticated user's Saved Messages
// chat (the self chat), the first external source of td import saved.
//
// Saved Messages is not a channel: it has no linked discussion group, so it
// can carry no machine records, and its items are addressed by saved message
// id alone. Saved Messages 2.0 partitions items into sub-chats; a saved
// message reports its sub-chat through Message.SavedPeerID.
type SavedClient interface {
	// StreamSavedHistory feeds saved messages newer than afterID to fn,
	// newest-first, covering the main saved chat and every sub-chat in one
	// walk. Returning an error from fn stops the stream. The HistoryMeta
	// describes whether the read provably covered everything down to the
	// afterID boundary.
	StreamSavedHistory(ctx context.Context, afterID int, fn func(Message) error) (HistoryMeta, error)
	// GetSavedMessage returns one saved message by id.
	GetSavedMessage(ctx context.Context, messageID int) (Message, error)
	// DownloadSavedMedia streams the media body of a saved message into dst.
	DownloadSavedMedia(ctx context.Context, messageID int, dst io.Writer) error
	// DeleteSavedMessage deletes one saved message. Saved Messages has no
	// tombstone form, so the delete is real and unrecoverable.
	DeleteSavedMessage(ctx context.Context, messageID int) error
}

// ThreadMessage is one message from a discussion-group history walk
// (StreamThreadHistory). The group contains two kinds of relevant messages:
// auto-forwarded channel post headers (RootMsgID > 0, PostID = original
// channel post id) and comments (Message with ReplyTo pointing at the
// thread root).
type ThreadMessage struct {
	Message
	// RootMsgID is the forwarded header's message id in the discussion
	// group; 0 for comments and unrelated chatter.
	RootMsgID int
	// PostID is the original channel post id a header was forwarded from;
	// 0 for comments.
	PostID int
}

// DiscussionClient manages the linked discussion group and its comment
// threads — the carrier of machine manifest records (ADR 0018). Channel
// operations are addressed by the DRIVE channel id; adapters resolve the
// linked group and thread roots internally.
type DiscussionClient interface {
	// EnsureDiscussionGroup creates and links a discussion supergroup for
	// the channel if none is linked, and returns the linked group.
	// Idempotent.
	EnsureDiscussionGroup(ctx context.Context, channelID int64) (*Channel, error)
	// LinkedDiscussionGroup resolves the linked discussion group. ok is
	// false when the channel has none.
	LinkedDiscussionGroup(ctx context.Context, channelID int64) (*Channel, bool, error)
	// SendThreadReply posts a comment on the post's comment thread and
	// returns the comment's message id (valid in the discussion group).
	SendThreadReply(ctx context.Context, channelID int64, postMsgID int, text string) (int, error)
	// EditThreadMessage edits a comment previously created by
	// SendThreadReply.
	EditThreadMessage(ctx context.Context, channelID int64, msgID int, text string) error
	// DeleteThreadMessage deletes a comment.
	DeleteThreadMessage(ctx context.Context, channelID int64, msgID int) error
	// StreamThreadHistory walks the discussion group newest-first, feeding
	// forwarded post headers and comments to fn. HistoryMeta describes
	// whether the read provably covered everything down to afterID.
	StreamThreadHistory(ctx context.Context, channelID int64, afterID int, fn func(ThreadMessage) error) (HistoryMeta, error)
}

// Typed errors.
type FloodWaitError struct {
	Seconds int
}

func (e *FloodWaitError) Error() string {
	return fmt.Sprintf("flood wait: retry after %s", (time.Duration(e.Seconds) * time.Second).String())
}

// CodeInvalidError means the login code was rejected after all retry attempts.
type CodeInvalidError struct{ Attempts int }

func (e *CodeInvalidError) Error() string {
	return fmt.Sprintf("login code invalid after %d attempts", e.Attempts)
}

// PasswordInvalidError means the 2FA password was rejected.
type PasswordInvalidError struct{}

func (e *PasswordInvalidError) Error() string { return "2FA password invalid" }

// CodeExpiredError means the login code expired and the automatic resend was
// already used up.
type CodeExpiredError struct{}

func (e *CodeExpiredError) Error() string {
	return "login code expired; run `td auth login` to request a fresh code"
}

// PhoneInvalidError means Telegram rejected the configured phone number. The
// number itself is deliberately not included: phone numbers are redacted
// everywhere else in the CLI output.
type PhoneInvalidError struct{}

func (e *PhoneInvalidError) Error() string {
	return "telegram rejected the configured phone number (telegram.phone): use international format, e.g. +15551234567"
}

type PermissionDeniedError struct{}

func (e *PermissionDeniedError) Error() string { return "permission denied" }

type MessageNotEditableError struct{}

func (e *MessageNotEditableError) Error() string { return "message not editable" }

type MessageNotFoundError struct{}

func (e *MessageNotFoundError) Error() string { return "message not found" }

type FileTooLargeError struct{}

func (e *FileTooLargeError) Error() string { return "file too large" }

type AuthRequiredError struct{}

// DiscussionMissingError means the channel has no linked discussion group,
// so machine manifest records cannot be written (ADR 0018).
type DiscussionMissingError struct{}

func (e *DiscussionMissingError) Error() string {
	return "channel has no linked discussion group; run: td channels link-discussion"
}

func (e *AuthRequiredError) Error() string { return "auth required" }
