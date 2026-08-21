package fake

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// TestTypedUploadRecordsKindAttributesThumb verifies the fake accepts and
// persists typed uploads the way native clients would observe them.
func TestTypedUploadRecordsKindAttributesThumb(t *testing.T) {
	c := New()
	loginFake(t, c)
	ctx := context.Background()
	ch, err := c.CreateChannel(ctx, "Typed")
	if err != nil {
		t.Fatal(err)
	}

	thumb := []byte{0xFF, 0xD8, 0xFF, 't', 'h', 'u', 'm', 'b'}
	up, err := c.UploadMedia(ctx, telegram.UploadRequest{
		ChannelID: ch.ID, FileName: "scene.mp4", MIME: "video/mp4", Size: 9,
		Reader: strings.NewReader("videobytes"),
		Kind:   telegram.KindVideo,
		Video:  &telegram.VideoAttributes{DurationSeconds: 97.5, Width: 1280, Height: 720, SupportsStreaming: true},
		Thumb:  thumb,
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := c.GetMessage(ctx, ch.ID, up.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Kind != telegram.KindVideo {
		t.Fatalf("kind = %q, want video", msg.Kind)
	}
	if msg.Video == nil || msg.Video.DurationSeconds != 97.5 || msg.Video.Width != 1280 ||
		msg.Video.Height != 720 || !msg.Video.SupportsStreaming {
		t.Fatalf("video attributes = %+v", msg.Video)
	}
	if string(msg.Thumb) != string(thumb) {
		t.Fatalf("thumb = %v", msg.Thumb)
	}
	if msg.FileName != "scene.mp4" || msg.MIME != "video/mp4" || string(msg.Data) != "videobytes" {
		t.Fatalf("document identity = %+v", msg)
	}

	// Photos read back as native photos: no filename, JPEG mime.
	upPhoto, err := c.UploadMedia(ctx, telegram.UploadRequest{
		ChannelID: ch.ID, FileName: "beach.jpg", Size: 6,
		Reader: strings.NewReader("pixels"),
		Kind:   telegram.KindPhoto,
	})
	if err != nil {
		t.Fatal(err)
	}
	photo, err := c.GetMessage(ctx, ch.ID, upPhoto.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if photo.Kind != telegram.KindPhoto || photo.MIME != "image/jpeg" || photo.FileName != "" {
		t.Fatalf("photo = kind %q mime %q name %q", photo.Kind, photo.MIME, photo.FileName)
	}

	// Unknown kinds are rejected, mirroring Telegram's input validation.
	if _, err := c.UploadMedia(ctx, telegram.UploadRequest{
		ChannelID: ch.ID, FileName: "x.bin", Size: 1,
		Reader: strings.NewReader("x"), Kind: "hologram",
	}); err == nil {
		t.Fatal("expected unsupported kind error")
	}

	// The default stays today's document-only send.
	upPlain, err := c.UploadMedia(ctx, telegram.UploadRequest{
		ChannelID: ch.ID, FileName: "a.bin", Size: 1, Reader: strings.NewReader("x"),
	})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := c.GetMessage(ctx, ch.ID, upPlain.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Kind != telegram.KindDocument || plain.Video != nil || plain.Thumb != nil {
		t.Fatalf("plain = kind %q video=%v thumb=%v", plain.Kind, plain.Video, plain.Thumb)
	}
}

// TestPersistentTypedStateSurvivesRestart proves the persisted fake state
// carries kinds, attributes, and thumbs across process restarts, which is
// what binary-level e2e tests rely on.
func TestPersistentTypedStateSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "fake.json")
	ctx := context.Background()

	c1 := NewPersistent(path)
	loginFake(t, c1)
	ch, err := c1.CreateChannel(ctx, "Typed Persist")
	if err != nil {
		t.Fatal(err)
	}
	up, err := c1.UploadMedia(ctx, telegram.UploadRequest{
		ChannelID: ch.ID, FileName: "clip.mp4", MIME: "video/mp4", Size: 4,
		Reader: strings.NewReader("body"),
		Kind:   telegram.KindVideo,
		Video:  &telegram.VideoAttributes{DurationSeconds: 3, Width: 10, Height: 10, SupportsStreaming: true},
		Thumb:  []byte("jpegbytes"),
	})
	if err != nil {
		t.Fatal(err)
	}

	c2 := NewPersistent(path)
	msg, err := c2.GetMessage(ctx, ch.ID, up.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Kind != telegram.KindVideo || msg.Video == nil || msg.Video.DurationSeconds != 3 ||
		!msg.Video.SupportsStreaming || string(msg.Thumb) != "jpegbytes" {
		t.Fatalf("restored message = kind %q video %+v thumb %q", msg.Kind, msg.Video, msg.Thumb)
	}
}
