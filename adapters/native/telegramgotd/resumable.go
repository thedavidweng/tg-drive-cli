package telegramgotd

import (
	"context"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/crypto"
	"github.com/gotd/td/tg"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/core/telegram"
	"golang.org/x/sync/errgroup"
)

// resumable upload constants matching Telegram docs.
const (
	resumablePartsLimit  = 3999
	resumableDefaultPart = 128 * 1024
	resumableMaxPartSize = 512 * 1024
)

// partAck retries for saveBigFilePart returning ok=false (Telegram asking to
// retry later): bounded, with backoff, so the CLI cannot busy-loop.
const partAckMaxRetries = 5

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

// bigFilePartSender is the seam for upload.saveBigFilePart. *tg.Client
// satisfies it directly; tests substitute fakes.
type bigFilePartSender interface {
	UploadSaveBigFilePart(ctx context.Context, request *tg.UploadSaveBigFilePartRequest) (bool, error)
}

var _ bigFilePartSender = (*tg.Client)(nil)

func resumableUploadBig(ctx context.Context, api bigFilePartSender, req tgtelegram.UploadRequest, store tgtelegram.ResumableStore, state *tgtelegram.UploadState) (tg.InputFileClass, error) {
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

	// The state mutex guards every read and copy of the shared state —
	// including the snapshot handed to the (serialized) persistence call — so
	// a save can never race a concurrent confirmation append.
	var mu sync.Mutex
	save := func() error {
		if store == nil {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
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

	err := g.Wait()
	if err != nil {
		// A part failed while others may still have been in flight: run one
		// final save on a background context so late confirmations are not
		// lost — the caller's ctx may already be cancelled.
		syncSaveOnBackground(store, req.ResumableKey, state, &mu)
		return nil, err
	}
	return &tg.InputFileBig{ID: state.FileID, Parts: totalParts, Name: req.FileName}, nil
}

// syncSaveOnBackground persists the current state snapshot best-effort with a
// background context after a failure. Callers must not hold mu.
func syncSaveOnBackground(store tgtelegram.ResumableStore, key string, state *tgtelegram.UploadState, mu *sync.Mutex) {
	if store == nil {
		return
	}
	mu.Lock()
	st := *state
	st.ConfirmedParts = append([]int(nil), state.ConfirmedParts...)
	sort.Ints(st.ConfirmedParts)
	mu.Unlock()
	_ = store.SaveUploadState(context.Background(), key, &st)
}

// uploadBigFilePart sends one part. A server ok=false acknowledgement means
// "resend later": retry a bounded number of times with backoff, honoring
// context cancellation, instead of looping forever.
func uploadBigFilePart(ctx context.Context, api bigFilePartSender, fileID int64, part, totalParts int, bytes []byte) error {
	backoff := 100 * time.Millisecond
	for attempt := 0; ; attempt++ {
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
		if attempt >= partAckMaxRetries {
			return errors.Errorf("upload part %d not confirmed after %d retries", part, partAckMaxRetries)
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		backoff *= 2
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
