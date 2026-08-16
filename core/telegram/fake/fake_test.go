package fake

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

func loginFake(t *testing.T, c *Client) {
	t.Helper()
	ctx := context.Background()
	_, err := c.Login(ctx, 1, "hash", "+1000",
		func(telegram.CodePrompt) (string, error) { return "12345", nil },
		func() (string, error) { return "", nil }, telegram.LoginOptions{})
	if err != nil {
		t.Fatal(err)
	}
}

// TestHistoryNewestFirst pins the ordering contract shared with the real
// adapter: history reads return messages newest-first, page semantics
// matching messages.getHistory. Scans depend on this order for
// newest-message-wins reconciliation.
func TestHistoryNewestFirst(t *testing.T) {
	c := New()
	loginFake(t, c)
	const ch = int64(1)
	for i := 0; i < 5; i++ {
		c.AddMessage(ch, telegram.Message{Kind: telegram.KindDocument, FileName: "f"})
	}
	msgs, err := c.History(context.Background(), ch, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 5 {
		t.Fatalf("messages = %d, want 5", len(msgs))
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i-1].ID <= msgs[i].ID {
			t.Fatalf("history not newest-first: %v then %v", msgs[i-1].ID, msgs[i].ID)
		}
	}
	// afterID keeps only strictly newer messages.
	msgs, err = c.History(context.Background(), ch, msgs[3].ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("messages after boundary = %d, want 3", len(msgs))
	}
}

func TestStreamHistoryCompleteAndTruncated(t *testing.T) {
	c := New()
	loginFake(t, c)
	const ch = int64(1)
	for i := 0; i < 10; i++ {
		c.AddMessage(ch, telegram.Message{Kind: telegram.KindDocument})
	}
	var seen []int
	meta, err := c.StreamHistory(context.Background(), ch, 0, func(m telegram.Message) error {
		seen = append(seen, m.ID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !meta.Complete {
		t.Fatal("healthy read reported incomplete")
	}
	if len(seen) != 10 {
		t.Fatalf("streamed %d messages, want 10", len(seen))
	}

	// The truncation knob simulates a Telegram pagination quirk: the newest
	// page arrives, the rest silently does not, and completeness is false.
	c.SetTruncateHistory(3)
	seen = nil
	meta, err = c.StreamHistory(context.Background(), ch, 0, func(m telegram.Message) error {
		seen = append(seen, m.ID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if meta.Complete {
		t.Fatal("truncated read reported complete")
	}
	if len(seen) != 3 {
		t.Fatalf("streamed %d messages, want 3", len(seen))
	}
}

func TestResumableUploadSendsOnlyUnconfirmedParts(t *testing.T) {
	c := New()
	loginFake(t, c)
	const ch = int64(1)
	c.SetPartSize(4)

	c.SetResumableThreshold(8) // fixture-sized files take the resumable path
	store := &memStore{}
	data := make([]byte, 16)
	for i := range data {
		data[i] = byte(i)
	}
	big := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(big, data, 0o644); err != nil {
		t.Fatal(err)
	}
	// Big-file requests carry a path and no reader, matching the service.
	req := telegram.UploadRequest{
		ChannelID: ch, FileName: "f.bin", Size: 16, Path: big,
		ResumableKey: "file:1", ResumableStore: store,
	}
	c.SetFailUploadAfterParts(2)
	if _, err := c.UploadMedia(context.Background(), req); err == nil {
		t.Fatal("expected interruption")
	}
	if len(store.saved) == 0 || len(store.saved[len(store.saved)-1].ConfirmedParts) != 2 {
		t.Fatalf("state not persisted for resume: %+v", store.saved)
	}
	c.ResetPartSubmissions()
	c.SetFailUploadAfterParts(0)
	res, err := c.UploadMedia(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if n := c.PartSubmissions(); n != 2 {
		t.Fatalf("resume submitted %d parts, want the 2 unconfirmed", n)
	}
	msg, err := c.GetMessage(context.Background(), ch, res.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.Data) != 16 {
		t.Fatalf("message data = %d bytes, want 16", len(msg.Data))
	}
}

// memStore is an in-memory ResumableStore.
type memStore struct {
	deleted bool
	saved   []*telegram.UploadState
}

func (m *memStore) LoadUploadState(context.Context, string) (*telegram.UploadState, error) {
	if len(m.saved) == 0 {
		return nil, nil
	}
	cp := *m.saved[len(m.saved)-1]
	return &cp, nil
}

func (m *memStore) SaveUploadState(_ context.Context, _ string, st *telegram.UploadState) error {
	cp := *st
	cp.ConfirmedParts = append([]int(nil), st.ConfirmedParts...)
	m.saved = append(m.saved, &cp)
	return nil
}

func (m *memStore) DeleteUploadState(context.Context, string) error {
	m.deleted = true
	m.saved = nil
	return nil
}
