package telegramgotd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gotd/td/tg"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// ackFlakySender returns ok=false for the first N calls per part, then ok.
type ackFlakySender struct {
	mu       sync.Mutex
	failNext int // global budget of ok=false answers
	calls    int
}

func (s *ackFlakySender) UploadSaveBigFilePart(ctx context.Context, req *tg.UploadSaveBigFilePartRequest) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.failNext > 0 {
		s.failNext--
		return false, nil
	}
	return true, nil
}

// ackNeverOkSender always answers ok=false.
type ackNeverOkSender struct{ calls int32 }

func (s *ackNeverOkSender) UploadSaveBigFilePart(ctx context.Context, req *tg.UploadSaveBigFilePartRequest) (bool, error) {
	atomic.AddInt32(&s.calls, 1)
	return false, nil
}

func TestUploadBigFilePartRetriesOkFalse(t *testing.T) {
	sender := &ackFlakySender{failNext: 3}
	if err := uploadBigFilePart(context.Background(), sender, 1, 0, 10, []byte("x")); err != nil {
		t.Fatalf("bounded retries should succeed: %v", err)
	}
	if sender.calls != 4 {
		t.Fatalf("calls = %d, want 4 (3 ok=false then success)", sender.calls)
	}
}

func TestUploadBigFilePartBoundedWhenAlwaysOkFalse(t *testing.T) {
	sender := &ackNeverOkSender{}
	err := uploadBigFilePart(context.Background(), sender, 1, 0, 10, []byte("x"))
	if err == nil {
		t.Fatal("expected bounded failure on persistent ok=false")
	}
	if got := atomic.LoadInt32(&sender.calls); got != int32(partAckMaxRetries+1) {
		t.Fatalf("calls = %d, want %d", got, partAckMaxRetries+1)
	}
}

func TestUploadBigFilePartHonorsContext(t *testing.T) {
	sender := &ackNeverOkSender{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := uploadBigFilePart(ctx, sender, 1, 0, 10, []byte("x"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// recordingStore counts saves; used by the race hammer.
type recordingStore struct {
	mu     sync.Mutex
	saves  int
	states []*tgtelegram.UploadState
}

func (s *recordingStore) LoadUploadState(context.Context, string) (*tgtelegram.UploadState, error) {
	return nil, nil
}

func (s *recordingStore) SaveUploadState(_ context.Context, _ string, st *tgtelegram.UploadState) error {
	cp := *st
	cp.ConfirmedParts = append([]int(nil), st.ConfirmedParts...)
	s.mu.Lock()
	s.saves++
	s.states = append(s.states, &cp)
	s.mu.Unlock()
	return nil
}

func (s *recordingStore) DeleteUploadState(context.Context, string) error { return nil }

func (s *recordingStore) snapshot() (int, []*tgtelegram.UploadState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saves, s.states
}

// TestResumableStateSaveUnderRace hammers the part-state machine with
// concurrent workers under the race detector: every persisted snapshot must
// be a consistent copy (monotonic confirmed bytes, no torn slices).
func TestResumableStateSaveUnderRace(t *testing.T) {
	const size = 512 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i)
	}
	path := filepath.Join(t.TempDir(), "race.bin")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	store := &recordingStore{}
	state := &tgtelegram.UploadState{
		FileID: 42, PartSize: 4 * 1024, TotalParts: size / (4 * 1024),
		TotalBytes: int64(size), ContentHash: "blake3:test",
	}
	req := tgtelegram.UploadRequest{
		FileName: "race.bin", Size: int64(size), Path: path, Threads: 8,
		ResumableKey: "file:1", ResumableStore: store,
	}
	if _, err := resumableUploadBig(context.Background(), ackOKSender{}, req, store, state); err != nil {
		t.Fatal(err)
	}
	saves, states := store.snapshot()
	if saves == 0 {
		t.Fatal("no state saved")
	}
	confirmed := map[int]bool{}
	for _, st := range states {
		if st.ConfirmedBytes < 0 || st.ConfirmedBytes > int64(size) {
			t.Fatalf("confirmed bytes out of range: %d", st.ConfirmedBytes)
		}
		for _, p := range st.ConfirmedParts {
			if p < 0 || p >= state.TotalParts {
				t.Fatalf("part index out of range: %d", p)
			}
			confirmed[p] = true
		}
	}
	if len(confirmed) != state.TotalParts {
		t.Fatalf("distinct confirmed parts = %d, want %d", len(confirmed), state.TotalParts)
	}
}

type ackOKSender struct{}

func (ackOKSender) UploadSaveBigFilePart(ctx context.Context, req *tg.UploadSaveBigFilePartRequest) (bool, error) {
	return true, nil
}
