package telegramgotd

import (
	"testing"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

func TestNormalizeMainSavedPeer(t *testing.T) {
	main := normalizeMainSavedPeer(telegram.Message{
		SavedPeerID:    42,
		SavedPeerTitle: "David",
	}, 42)
	if main.SavedPeerID != 0 || main.SavedPeerTitle != "" {
		t.Fatalf("main saved peer = %+v", main)
	}

	forwarded := normalizeMainSavedPeer(telegram.Message{
		SavedPeerID: 42,
		Forward:     &telegram.ForwardOrigin{FromID: 42},
	}, 42)
	if forwarded.SavedPeerID != 42 {
		t.Fatalf("forwarded saved peer was normalized: %+v", forwarded)
	}

	subchat := normalizeMainSavedPeer(telegram.Message{
		SavedPeerID:    99,
		SavedPeerTitle: "Trips",
	}, 42)
	if subchat.SavedPeerID != 99 || subchat.SavedPeerTitle != "Trips" {
		t.Fatalf("sub-chat peer changed: %+v", subchat)
	}
}
