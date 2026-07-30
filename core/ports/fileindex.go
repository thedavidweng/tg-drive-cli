package ports

import (
	"context"

	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/pathcodec"
)

// FileIndex is the repository seam for the file index. It persists the file
// row, slug map, hashtag tags, and derived directory nodes.
type FileIndex interface {
	Index(ctx context.Context, req FileIndexRequest) (fileID int64, err error)
}

// FileIndexRequest is the unit of work for FileIndex.Index.
type FileIndexRequest struct {
	ChannelRowID  int64
	FileID        int64
	MessageID     int
	ManifestMsgID int
	Meta          manifest.FileMeta
	SlugMaps      []pathcodec.SlugMapping
	Tags          []string
	ReplaceFileID int64
	SetUploadedAt bool
	Now           string
}
