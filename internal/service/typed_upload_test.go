package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/core/manifest"
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

// TestZeroPresentationMatchesPlainSend pins the compatibility invariant:
// uploading without presentation hints produces exactly today's document
// send — no attribute block, no thumb.

// TestPhotoUploadNativePresentation checks the photo kind publishes a native
// photo message as native clients see it, and that it downloads back through
// the normal path (offline the fake models photo read-back verbatim; real
// Telegram serves its recompressed largest representation).

// TestNativePhotoDownloadServesRecompressedBytes pins the photo download
// contract: Telegram serves its own recompressed representation for native
// photos, so stored size/hash of the original bytes must not fail
// verification. Documents keep strict verification — their bytes are
// untouched by the platform.

// TestTypedUploadContentIdentity proves byte fidelity end to end: a
// video-kind upload downloads back bit-identical, hash intact.

// TestPresentationValidateRejectsInvalid pins the typed-upload failure modes
// to the usage error code so orchestration stays table-driven.

// TestTypedUploadCaptionBudgetIdentical proves caption budget enforcement
// fails a typed upload exactly like a plain one, for every kind. The display
// name alone overflows the minimal manifest-reply caption, so RenderCaption
// errors.

// TestTypedUploadResumeAdoptsPendingRow covers the pending-row lifecycle for
// typed uploads of every kind: an interrupted resumable upload leaves an
// adoptable pending row whose retry carries the same presentation.
func TestTypedUploadResumeAdoptsPendingRow(t *testing.T) {
	cases := []struct {
		name  string
		pres  Presentation
		check func(t *testing.T, msg telegram.Message)
	}{
		{
			name: "photo",
			pres: Presentation{Kind: telegram.KindPhoto},
			check: func(t *testing.T, msg telegram.Message) {
				t.Helper()
				if msg.Kind != telegram.KindPhoto || msg.Video != nil {
					t.Fatalf("resumed message lost presentation: kind=%q video=%+v", msg.Kind, msg.Video)
				}
			},
		},
		{
			name: "video",
			pres: Presentation{
				Kind:              telegram.KindVideo,
				DurationSeconds:   600,
				Width:             1920,
				Height:            1080,
				SupportsStreaming: true,
			},
			check: func(t *testing.T, msg telegram.Message) {
				t.Helper()
				if msg.Kind != telegram.KindVideo || msg.Video == nil || msg.Video.DurationSeconds != 600 || !msg.Video.SupportsStreaming {
					t.Fatalf("resumed message lost presentation: kind=%q video=%+v", msg.Kind, msg.Video)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, tg := testApp(t)
			loginAndInit(t, app, tg)
			ctx := context.Background()

			big := filepath.Join(t.TempDir(), "big.bin")
			if err := os.WriteFile(big, bytes.Repeat([]byte{0xA5}, 11*1024*1024), 0o644); err != nil {
				t.Fatal(err)
			}
			tg.SetPartSize(1024 * 1024)
			tg.SetFailUploadAfterParts(2)
			if _, err := app.UploadFileAs(ctx, big, "/media/big.bin", ConflictFail, false, tc.pres); err == nil {
				t.Fatal("expected interrupted upload to fail")
			}
			if got := fileStatus(t, app, "/media/big.bin"); got != "pending" {
				t.Fatalf("status after interruption = %q, want pending", got)
			}

			tg.SetFailUploadAfterParts(0)
			tg.ResetPartSubmissions()
			data, err := app.UploadFileAs(ctx, big, "/media/big.bin", ConflictFail, false, tc.pres)
			if err != nil {
				t.Fatal(err)
			}
			if data["resumed"] != true {
				t.Fatalf("retry data = %v, want resumed:true", data)
			}
			tgChID, _ := app.tgChannelID(ctx)
			msg := messageByID(t, tg, tgChID, data["message_id"].(int))
			tc.check(t, msg)
			if got := fileStatus(t, app, "/media/big.bin"); got != "active" {
				t.Fatalf("status after resume = %q, want active", got)
			}
		})
	}
}

// TestTypedUploadScanReconstruction follows the established lifecycle:
// publish typed content, wipe the index, scan --full, verify reconstruction.
// A published photo must index exactly like an adopted native photo — same
// columns populated from the caption (path, size, hash).
func TestTypedUploadScanReconstruction(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)

	content := []byte("reconstruct me")
	adopted := []byte("adopted native jpeg")
	paths := map[string][]byte{
		"/media/scene.mp4":  content,
		"/docs/note.txt":    []byte("plain"),
		"/pics/beach.jpg":   []byte("pixels"),
		"/pics/adopted.jpg": adopted,
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
	if _, err := app.UploadFileAs(ctx, writeLocal(t, "pixels"), "/pics/beach.jpg", ConflictFail, false, Presentation{Kind: telegram.KindPhoto}); err != nil {
		t.Fatal(err)
	}
	tg.AddMessage(tgChID, telegram.Message{
		ID: 90, Kind: telegram.KindPhoto, MIME: "image/jpeg",
		Caption: manifest.RenderCompact(manifest.FileMeta{
			CanonicalPath: "/pics/adopted.jpg",
			DisplayName:   "adopted.jpg",
			Size:          int64(len(adopted)),
			Hash:          blake3Hex(adopted),
			MIME:          "image/jpeg",
		}),
		Data: adopted,
	})

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

	// Published and adopted photos land in the index identically: path,
	// size, and provenance hash all populated from their captions.
	channelID, _, err := app.channelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string][]byte{"/pics/beach.jpg": []byte("pixels"), "/pics/adopted.jpg": adopted} {
		var size int64
		var hash string
		if err := app.DB.Raw().QueryRowContext(ctx,
			`select size, content_hash from files where channel_id=? and canonical_path=? and status='active'`,
			channelID, path).Scan(&size, &hash); err != nil {
			t.Fatalf("%s missing from rebuilt index: %v", path, err)
		}
		if size != int64(len(want)) || hash != blake3Hex(want) {
			t.Fatalf("%s indexed as size=%d hash=%q, want %d %s", path, size, hash, len(want), blake3Hex(want))
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
	photoDest := filepath.Join(t.TempDir(), "beach.jpg")
	if _, err := app.DownloadFile(ctx, "/pics/beach.jpg", photoDest, ConflictFail); err != nil {
		t.Fatal(err)
	}
	pgot, err := os.ReadFile(photoDest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pgot, paths["/pics/beach.jpg"]) {
		t.Fatal("reconstructed photo content mismatch")
	}
}
