package telegramgotd

import (
	"context"
	"io"
	"os"
	"sort"
	"sync"

	"github.com/go-faster/errors"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/crypto"
	"github.com/gotd/td/tg"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/core/telegram"
	"golang.org/x/sync/errgroup"
)

// resumable upload constants matching Telegram docs.
const (
	resumableBigFileLimit = 10 * 1024 * 1024
	resumablePartsLimit   = 3999
	resumableDefaultPart  = 128 * 1024
	resumableMaxPartSize  = 512 * 1024
)

func resumableComputePartSize(total int64) int {
	partSize := resumableDefaultPart
	for partSize < resumableMaxPartSize {
		parts := (total + int64(partSize) - 1) / int64(partSize)
		if parts <= resumablePartsLimit {
			break
		}
		partSize *= 2
	}
	if partSize > resumableMaxPartSize {
		partSize = resumableMaxPartSize
	}
	return partSize
}

type partRange struct {
	id     int
	offset int64
	size   int
}

func resumableUploadBig(ctx context.Context, api *tg.Client, req tgtelegram.UploadRequest, store tgtelegram.ResumableStore, state *tgtelegram.UploadState) (tg.InputFileClass, error) {
	if req.Path == "" {
		return nil, errors.New("resumable big upload requires a local file path")
	}
	partSize := state.PartSize
	totalParts := state.TotalParts
	if partSize <= 0 {
		partSize = resumableComputePartSize(req.Size)
		totalParts = int((req.Size + int64(partSize) - 1) / int64(partSize))
		state.PartSize = partSize
		state.TotalParts = totalParts
	}

	confirmed := make(map[int]bool, len(state.ConfirmedParts))
	for _, p := range state.ConfirmedParts {
		confirmed[p] = true
	}

	var parts []partRange
	for i := 0; i < totalParts; i++ {
		if confirmed[i] {
			continue
		}
		size := partSize
		if i == totalParts-1 {
			size = int(req.Size - int64(i*partSize))
		}
		parts = append(parts, partRange{id: i, offset: int64(i * partSize), size: size})
	}

	threads := req.Threads
	if threads <= 0 {
		threads = 4
	}

	pool := bin.NewPool(partSize)

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(threads)

	var mu sync.Mutex
	save := func() error {
		if store == nil {
			return nil
		}
		st := *state
		st.ConfirmedParts = append([]int(nil), state.ConfirmedParts...)
		sort.Ints(st.ConfirmedParts)
		return store.SaveUploadState(ctx, req.ResumableKey, &st)
	}

	for _, p := range parts {
		p := p
		g.Go(func() error {
			f, err := os.Open(req.Path)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			if _, err := f.Seek(p.offset, io.SeekStart); err != nil {
				return err
			}
			buf := pool.GetSize(p.size)
			defer pool.Put(buf)
			if _, err := io.ReadFull(f, buf.Buf); err != nil {
				return err
			}
			buf.Buf = buf.Buf[:p.size]

			if err := uploadBigFilePart(ctx, api, state.FileID, p.id, totalParts, buf.Buf); err != nil {
				return err
			}

			if req.Progress != nil {
				if err := req.Progress(ctx, tgtelegram.UploadProgressState{
					FileName: req.FileName,
					Part:     p.id,
					PartSize: partSize,
					Uploaded: p.offset + int64(p.size),
					Total:    req.Size,
				}); err != nil {
					return err
				}
			}

			mu.Lock()
			state.ConfirmedParts = append(state.ConfirmedParts, p.id)
			state.ConfirmedBytes += int64(p.size)
			mu.Unlock()
			return save()
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return &tg.InputFileBig{ID: state.FileID, Parts: totalParts, Name: req.FileName}, nil
}

func uploadBigFilePart(ctx context.Context, api *tg.Client, fileID int64, part, totalParts int, bytes []byte) error {
	for {
		ok, err := api.UploadSaveBigFilePart(ctx, &tg.UploadSaveBigFilePartRequest{
			FileID:         fileID,
			FilePart:       part,
			FileTotalParts: totalParts,
			Bytes:          bytes,
		})
		if err != nil {
			return mapRPCError(err)
		}
		if ok {
			return nil
		}
	}
}

// cryptoRandFileID returns a positive random int64 using gotd's crypto helper.
func cryptoRandFileID() (int64, error) {
	for {
		id, err := crypto.RandInt64(crypto.DefaultRand())
		if err != nil {
			return 0, err
		}
		if id > 0 {
			return id, nil
		}
	}
}
