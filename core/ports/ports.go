package ports

import (
	"context"
	"io"
	"time"

	"github.com/thedavidweng/tg-drive-cli/core/model"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// Store is the persistence port for drive metadata. Native SQLite and browser
// IndexedDB adapters implement this interface in later stages.
type Store interface {
	LoadSlugMap(ctx context.Context, channelID model.ChannelID) (map[string]string, error)
}

// Telegram is the remote channel/media port.
type Telegram interface {
	UploadMedia(ctx context.Context, req telegram.UploadRequest) (*telegram.UploadResult, error)
	DownloadMedia(ctx context.Context, channelID int64, messageID int, dst io.Writer) error
}

// Clock provides time for testable use cases.
type Clock interface {
	Now() time.Time
}
