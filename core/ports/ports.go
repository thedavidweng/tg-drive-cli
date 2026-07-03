package ports

import (
	"context"
	"io"
	"time"

	"github.com/thedavidweng/tg-drive-cli/core/model"
)

// Store is the persistence port for drive metadata. Native SQLite and browser
// IndexedDB adapters implement this interface in later stages.
type Store interface {
	LoadSlugMap(ctx context.Context, channelID model.ChannelID) (map[string]string, error)
}

// Telegram is the remote channel/media port. Implementations must support
// streaming download for large files.
type Telegram interface {
	UploadMedia(ctx context.Context, req UploadRequest) (UploadResult, error)
	DownloadMedia(ctx context.Context, channelID int64, messageID int, dst io.Writer) error
}

// UploadRequest is a media upload request for the Telegram port.
type UploadRequest struct {
	ChannelID int64
	Caption   string
	FileName  string
	MIME      string
	Size      int64
	Reader    io.Reader
}

// UploadResult is returned after a successful upload.
type UploadResult struct {
	MessageID int
}

// Clock provides time for testable use cases.
type Clock interface {
	Now() time.Time
}
