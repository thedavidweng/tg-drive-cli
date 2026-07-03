package model

// CanonicalPath is a normalized remote path starting with "/".
type CanonicalPath string

// ChannelID is the internal SQLite channel row id.
type ChannelID int64

// FileID is the internal SQLite files row id.
type FileID int64

// MessageID is a Telegram channel message id.
type MessageID int

// FileStatus is the lifecycle state of an indexed file row.
type FileStatus string

const (
	FileStatusPending    FileStatus = "pending"
	FileStatusActive     FileStatus = "active"
	FileStatusDeleted    FileStatus = "deleted"
	FileStatusSuperseded FileStatus = "superseded"
	FileStatusMissing    FileStatus = "missing"
	FileStatusInvalid    FileStatus = "invalid"
	FileStatusOrphaned   FileStatus = "orphaned"
)
