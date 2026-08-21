package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"github.com/thedavidweng/tg-drive-cli/core/telegram/fake"
	"lukechampine.com/blake3"
)

// jpegHeader is a minimal JPEG-looking thumbnail body; adapters store bytes
// without decoding them.
var jpegHeader = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x01}

func blake3Hex(b []byte) string {
	h := blake3.New(32, nil)
	_, _ = h.Write(b)
	return "blake3:" + hex.EncodeToString(h.Sum(nil))
}

func messageByID(t *testing.T, tg *fake.Client, chID int64, msgID int) telegram.Message {
	t.Helper()
	for _, m := range tg.Messages(chID) {
		if m.ID == msgID {
			return m
		}
	}
	t.Fatalf("message %d not found in channel %d", msgID, chID)
	return telegram.Message{}
}

func writeThumb(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "poster.jpg")
	if err := os.WriteFile(p, jpegHeader, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestTypedVideoUploadCarriesPresentation asserts the observable state of the
// published message: kind, video attribute block, thumbnail bytes, caption,
// and untouched payload.
func TestTypedVideoUploadCarriesPresentation(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	content := []byte("fake mp4 byte stream")
	local := writeLocal(t, string(content))
	data, err := app.UploadFileAs(ctx, local, "/media/scene.mp4", ConflictFail, false, Presentation{
		Kind:              telegram.KindVideo,
		DurationSeconds:   97.5,
		Width:             1280,
		Height:            720,
		SupportsStreaming: true,
		ThumbPath:         writeThumb(t),
	})
	if err != nil {
		t.Fatal(err)
	}

	tgChID, _ := app.tgChannelID(ctx)
	msg := messageByID(t, tg, tgChID, data["message_id"].(int))
	if msg.Kind != telegram.KindVideo {
		t.Fatalf("kind = %q, want video", msg.Kind)
	}
	if msg.Video == nil {
		t.Fatal("video attribute block missing")
	}
	if msg.Video.DurationSeconds != 97.5 || msg.Video.Width != 1280 || msg.Video.Height != 720 || !msg.Video.SupportsStreaming {
		t.Fatalf("video attributes = %+v", msg.Video)
	}
	if !bytes.Equal(msg.Thumb, jpegHeader) {
		t.Fatalf("thumb = %v", msg.Thumb)
	}
	if !bytes.Equal(msg.Data, content) {
		t.Fatal("payload bytes were modified")
	}
	if msg.FileName != "scene.mp4" || msg.MIME != detectMIME(local) {
		t.Fatalf("file identity = %s %s", msg.MIME, msg.FileName)
	}
	if !strings.Contains(msg.Caption, "td:v1") {
		t.Fatalf("caption lost machine meta: %q", msg.Caption)
	}
	if got := fileStatus(t, app, "/media/scene.mp4"); got != "active" {
		t.Fatalf("status = %q, want active", got)
	}
	if data["hash"] != blake3Hex(content) {
		t.Fatalf("hash = %v, want %s", data["hash"], blake3Hex(content))
	}
}

// TestZeroPresentationMatchesPlainSend pins the compatibility invariant:
// uploading without presentation hints produces exactly today's document
// send — no attribute block, no thumb.
func TestZeroPresentationMatchesPlainSend(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	content := "identical"

	if _, err := app.UploadFile(ctx, writeLocal(t, content), "/plain.bin", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFileAs(ctx, writeLocal(t, content), "/typed.bin", ConflictFail, false, Presentation{}); err != nil {
		t.Fatal(err)
	}

	tgChID, _ := app.tgChannelID(ctx)
	msgs := tg.Messages(tgChID)
	var plain, typed telegram.Message
	for _, m := range msgs {
		switch m.FileName {
		case "plain.bin":
			plain = m
		case "typed.bin":
			typed = m
		}
	}
	if plain.ID == 0 || typed.ID == 0 {
		t.Fatalf("messages not found: %+v", msgs)
	}
	if plain.Kind != telegram.KindDocument || typed.Kind != telegram.KindDocument {
		t.Fatalf("kinds = %q / %q", plain.Kind, typed.Kind)
	}
	if typed.Video != nil || typed.Thumb != nil {
		t.Fatalf("zero presentation must not attach attributes: video=%v thumb=%v", typed.Video, typed.Thumb)
	}
	if plain.MIME != typed.MIME || plain.FileSize != typed.FileSize || plain.Caption == "" || typed.Caption == "" {
		t.Fatalf("plain=%+v typed=%+v", plain, typed)
	}
}

// TestPhotoUploadNativePresentation checks the photo kind publishes a native
// photo message as native clients see it.
func TestPhotoUploadNativePresentation(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	data, err := app.UploadFileAs(ctx, writeLocal(t, "pixels"), "/pics/beach.jpg", ConflictFail, false, Presentation{Kind: telegram.KindPhoto})
	if err != nil {
		t.Fatal(err)
	}
	tgChID, _ := app.tgChannelID(ctx)
	msg := messageByID(t, tg, tgChID, data["message_id"].(int))
	if msg.Kind != telegram.KindPhoto {
		t.Fatalf("kind = %q, want photo", msg.Kind)
	}
	if msg.MIME != "image/jpeg" || msg.FileName != "" {
		t.Fatalf("photo identity = mime %q name %q", msg.MIME, msg.FileName)
	}
	if msg.Video != nil || msg.Thumb != nil {
		t.Fatal("photos carry no attribute block or thumb")
	}
	if !strings.Contains(msg.Caption, "td:v1") {
		t.Fatalf("caption lost machine meta: %q", msg.Caption)
	}
}

// TestTypedUploadContentIdentity proves byte fidelity end to end: a
// video-kind upload downloads back bit-identical, hash intact.
func TestTypedUploadContentIdentity(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	content := []byte(strings.Repeat("0123456789abcdef", 64))
	data, err := app.UploadFileAs(ctx, writeLocal(t, string(content)), "/media/clip.mp4", ConflictFail, false, Presentation{
		Kind:              telegram.KindVideo,
		DurationSeconds:   12,
		Width:             640,
		Height:            360,
		SupportsStreaming: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "clip.mp4")
	if _, err := app.DownloadFile(ctx, "/media/clip.mp4", dest, ConflictFail); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("downloaded bytes differ from source")
	}
	if data["hash"] != blake3Hex(content) {
		t.Fatalf("hash = %v, want %s", data["hash"], blake3Hex(content))
	}
}

// TestPresentationValidateRejectsInvalid pins the typed-upload failure modes
// to the usage error code so orchestration stays table-driven.
func TestPresentationValidateRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		pres Presentation
	}{
		{"unknown kind", Presentation{Kind: "audio"}},
		{"text kind", Presentation{Kind: telegram.KindText}},
		{"negative duration", Presentation{Kind: telegram.KindVideo, DurationSeconds: -1}},
		{"negative width", Presentation{Kind: telegram.KindVideo, Width: -1}},
		{"negative height", Presentation{Kind: telegram.KindVideo, Height: -2}},
		{"attributes without video kind", Presentation{Width: 100}},
		{"streaming without video kind", Presentation{SupportsStreaming: true}},
		{"thumb with photo kind", Presentation{Kind: telegram.KindPhoto, ThumbPath: "poster.jpg"}},
	}
	for _, tc := range cases {
		err := tc.pres.Validate()
		if code := appErrCode(t, err); code != apperr.ErrUsage {
			t.Fatalf("%s: code = %s, want ERR_USAGE", tc.name, code)
		}
	}
	for _, pres := range []Presentation{
		{},
		{Kind: telegram.KindDocument},
		{Kind: telegram.KindPhoto},
		{Kind: telegram.KindVideo},
		{Kind: telegram.KindVideo, DurationSeconds: 3.5, Width: 1, Height: 1, SupportsStreaming: true, ThumbPath: "t.jpg"},
		{Kind: telegram.KindDocument, ThumbPath: "t.jpg"},
	} {
		if err := pres.Validate(); err != nil {
			t.Fatalf("%+v: unexpected error %v", pres, err)
		}
	}
}

// TestTypedUploadCaptionBudgetIdentical proves caption budget enforcement
// fails a typed upload exactly like a plain one. The display name alone
// overflows the minimal manifest-reply caption, so RenderCaption errors.
func TestTypedUploadCaptionBudgetIdentical(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	dest := "/" + strings.Repeat("x", 2048) + ".mp4"

	_, plainErr := app.UploadFile(ctx, writeLocal(t, "x"), dest, ConflictFail, false)
	if code := appErrCode(t, plainErr); code != apperr.ErrCaptionTooLong {
		t.Fatalf("plain code = %s, want ERR_CAPTION_TOO_LONG", code)
	}
	_, typedErr := app.UploadFileAs(ctx, writeLocal(t, "x"), dest, ConflictFail, false, Presentation{
		Kind: telegram.KindVideo, DurationSeconds: 5, Width: 10, Height: 10,
	})
	if code := appErrCode(t, typedErr); code != apperr.ErrCaptionTooLong {
		t.Fatalf("typed code = %s, want ERR_CAPTION_TOO_LONG", code)
	}
}

// TestTypedUploadResumeAdoptsPendingRow covers the pending-row lifecycle for
// typed uploads: an interrupted resumable video upload leaves an adoptable
// pending row whose retry carries the same presentation.
func TestTypedUploadResumeAdoptsPendingRow(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	big := filepath.Join(t.TempDir(), "big.mp4")
	if err := os.WriteFile(big, bytes.Repeat([]byte{0xA5}, 11*1024*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	tg.SetPartSize(1024 * 1024)
	tg.SetFailUploadAfterParts(2)
	pres := Presentation{
		Kind:              telegram.KindVideo,
		DurationSeconds:   600,
		Width:             1920,
		Height:            1080,
		SupportsStreaming: true,
	}
	if _, err := app.UploadFileAs(ctx, big, "/media/big.mp4", ConflictFail, false, pres); err == nil {
		t.Fatal("expected interrupted upload to fail")
	}
	if got := fileStatus(t, app, "/media/big.mp4"); got != "pending" {
		t.Fatalf("status after interruption = %q, want pending", got)
	}

	tg.SetFailUploadAfterParts(0)
	tg.ResetPartSubmissions()
	data, err := app.UploadFileAs(ctx, big, "/media/big.mp4", ConflictFail, false, pres)
	if err != nil {
		t.Fatal(err)
	}
	if data["resumed"] != true {
		t.Fatalf("retry data = %v, want resumed:true", data)
	}
	tgChID, _ := app.tgChannelID(ctx)
	msg := messageByID(t, tg, tgChID, data["message_id"].(int))
	if msg.Kind != telegram.KindVideo || msg.Video == nil || msg.Video.DurationSeconds != 600 || !msg.Video.SupportsStreaming {
		t.Fatalf("resumed message lost presentation: kind=%q video=%+v", msg.Kind, msg.Video)
	}
	if got := fileStatus(t, app, "/media/big.mp4"); got != "active" {
		t.Fatalf("status after resume = %q, want active", got)
	}
}

// TestTypedUploadScanReconstruction follows the established lifecycle:
// publish typed content, wipe the index, scan --full, verify reconstruction.
func TestTypedUploadScanReconstruction(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	content := []byte("reconstruct me")
	paths := map[string][]byte{
		"/media/scene.mp4": content,
		"/docs/note.txt":   []byte("plain"),
	}
	if _, err := app.UploadFileAs(ctx, writeLocal(t, string(content)), "/media/scene.mp4", ConflictFail, false, Presentation{
		Kind:              telegram.KindVideo,
		DurationSeconds:   42,
		Width:             3840,
		Height:            2160,
		SupportsStreaming: true,
		ThumbPath:         writeThumb(t),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, writeLocal(t, "plain"), "/docs/note.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"path_tags", "path_segment_slugs", "files", "nodes", "scan_state"} {
		if _, err := app.DB.Raw().Exec(`delete from ` + table); err != nil {
			t.Fatalf("wipe %s: %v", table, err)
		}
	}
	res, err := app.Scan(ctx, ScanOptions{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if res["active"].(int) != len(paths) {
		t.Fatalf("active = %v, want %d", res["active"], len(paths))
	}
	for p := range paths {
		if got := fileStatus(t, app, p); got != "active" {
			t.Fatalf("%s status = %q after rebuild", p, got)
		}
	}
	dest := filepath.Join(t.TempDir(), "out.bin")
	dl, err := app.DownloadFile(ctx, "/media/scene.mp4", dest, ConflictFail)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) || dl.Size != int64(len(content)) {
		t.Fatalf("reconstructed content mismatch: size=%d", dl.Size)
	}
}
