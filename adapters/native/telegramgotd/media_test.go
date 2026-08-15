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
