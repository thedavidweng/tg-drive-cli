package telegramgotd

import (
	"context"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// mediaUploader uploads file bytes to Telegram and returns an InputFile.
type mediaUploader interface {
	upload(ctx context.Context, api *tg.Client, req tgtelegram.UploadRequest) (tg.InputFileClass, error)
}

// smallUploader handles files below the resumable threshold.
type smallUploader struct{}

func (smallUploader) upload(ctx context.Context, api *tg.Client, req tgtelegram.UploadRequest) (tg.InputFileClass, error) {
	up := uploader.NewUploader(api)
	if req.Threads > 0 {
		up = up.WithThreads(req.Threads)
	}
	if req.PartSize > 0 {
		up = up.WithPartSize(req.PartSize)
	}
	if req.Progress != nil {
		up = up.WithProgress(&uploadProgress{cb: req.Progress})
	}
	return up.Upload(ctx, uploader.NewUpload(req.FileName, req.Reader, req.Size))
}

// bigUploader handles resumable big-file uploads.
type bigUploader struct{}

func (bigUploader) upload(ctx context.Context, api *tg.Client, req tgtelegram.UploadRequest) (tg.InputFileClass, error) {
	if req.Path == "" {
		return nil, errors.New("resumable big upload requires a local file path")
	}
	state, err := req.ResumableStore.LoadUploadState(ctx, req.ResumableKey)
	if err != nil {
		state = nil
	}
	if state != nil && (state.TotalBytes != req.Size || state.ContentHash != req.ContentHash || state.PartSize == 0) {
		state = nil
	}
	if state == nil {
		id, err := cryptoRandFileID()
		if err != nil {
			return nil, err
		}
		partSize := resumableComputePartSize(req.Size)
		totalParts := int((req.Size + int64(partSize) - 1) / int64(partSize))
		state = &tgtelegram.UploadState{
			FileID:      id,
			PartSize:    partSize,
			TotalParts:  totalParts,
			TotalBytes:  req.Size,
			ContentHash: req.ContentHash,
		}
	}
	uploaded, err := resumableUploadBig(ctx, api, req, req.ResumableStore, state)
	if err != nil {
		return nil, err
	}
	_ = req.ResumableStore.DeleteUploadState(ctx, req.ResumableKey)
	return uploaded, nil
}

// selectMediaUploader picks the right uploader for a request.
func selectMediaUploader(req tgtelegram.UploadRequest) mediaUploader {
	if req.ResumableKey != "" && req.Size > resumableBigFileLimit && req.ResumableStore != nil && req.Path != "" {
		return bigUploader{}
	}
	return smallUploader{}
}
