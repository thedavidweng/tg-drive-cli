package telegramgotd

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestExtractMessageIDUpdates(t *testing.T) {
	id, err := extractMessageID(&tg.Updates{
		Updates: []tg.UpdateClass{
			&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 42}},
		},
	})
	if err != nil || id != 42 {
		t.Fatalf("updates id=%d err=%v", id, err)
	}
}

func TestExtractMessageIDUpdatesCombined(t *testing.T) {
	id, err := extractMessageID(&tg.UpdatesCombined{
		Updates: []tg.UpdateClass{
			&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 77}},
		},
	})
	if err != nil || id != 77 {
		t.Fatalf("combined id=%d err=%v", id, err)
	}
}

func TestExtractMessageIDShortSent(t *testing.T) {
	id, err := extractMessageID(&tg.UpdateShortSentMessage{ID: 9})
	if err != nil || id != 9 {
		t.Fatalf("short id=%d err=%v", id, err)
	}
}

func TestMessageFromTGGroupedID(t *testing.T) {
	msg := &tg.Message{ID: 10, Message: "#tag dump"}
	msg.SetGroupedID(99)
	msg.Media = &tg.MessageMediaPhoto{Photo: &tg.Photo{}}
	got := messageFromTG(msg)
	if got.GroupedID != 99 {
		t.Fatalf("grouped=%d", got.GroupedID)
	}
	if got.Caption != "#tag dump" {
		t.Fatalf("caption=%q", got.Caption)
	}
}

func TestEditMessageRequestEmptySetsFlag(t *testing.T) {
	req := &tg.MessagesEditMessageRequest{
		Peer: &tg.InputPeerChannel{ChannelID: 1, AccessHash: 2},
		ID:   1,
	}
	req.SetMessage("")
	if !req.Flags.Has(11) {
		t.Fatal("empty caption must set the message flag so Telegram clears the text")
	}
	if msg, ok := req.GetMessage(); !ok || msg != "" {
		t.Fatalf("message=%q ok=%v", msg, ok)
	}
}

// TestLargestPhotoType pins the photo download contract: native photos are
// fetched as the largest downloadable raster representation, ignoring
// preview-only size entries.
func TestLargestPhotoType(t *testing.T) {
	photo := &tg.Photo{Sizes: []tg.PhotoSizeClass{
		&tg.PhotoSize{Type: "s", W: 100, H: 100},
		&tg.PhotoSizeProgressive{Type: "x", W: 800, H: 600, Sizes: []int{1000, 2000}},
		&tg.PhotoSize{Type: "y", W: 1280, H: 960},
		&tg.PhotoPathSize{Type: "j"}, // SVG preview, not a downloadable raster
	}}
	if got := largestPhotoType(photo); got != "y" {
		t.Fatalf("largest = %q, want y", got)
	}

	progressive := &tg.Photo{Sizes: []tg.PhotoSizeClass{
		&tg.PhotoSize{Type: "m", W: 320, H: 240},
		&tg.PhotoSizeProgressive{Type: "w", W: 2560, H: 1440, Sizes: []int{10, 20, 30}},
	}}
	if got := largestPhotoType(progressive); got != "w" {
		t.Fatalf("largest = %q, want w", got)
	}

	// No raster sizes at all: fall back to the full-quality type.
	if got := largestPhotoType(&tg.Photo{}); got != "y" {
		t.Fatalf("largest = %q, want y", got)
	}
}
