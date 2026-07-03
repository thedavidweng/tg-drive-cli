package telegramgotd

import "testing"

func TestFormatTDChannelTitle(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"Pictures", "Pictures [TD]"},
		{"  My Drive  ", "My Drive [TD]"},
		{"Already [TD]", "Already [TD]"},
		{"already [td]", "already [td]"},
	}
	for _, tc := range tests {
		if got := formatTDChannelTitle(tc.in); got != tc.want {
			t.Fatalf("formatTDChannelTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTDChannelAbout(t *testing.T) {
	const want = "Telegram Drive Storage Folder\n[telegram-drive-folder]"
	if tdChannelAbout != want {
		t.Fatalf("tdChannelAbout = %q, want %q", tdChannelAbout, want)
	}
}

func TestChannelTitleMatches(t *testing.T) {
	if !channelTitleMatches("Pictures", "Pictures [TD]") {
		t.Fatal("expected bare name to match TD channel title")
	}
	if !channelTitleMatches("Pictures [TD]", "Pictures [TD]") {
		t.Fatal("expected exact title match")
	}
	if channelTitleMatches("Other", "Pictures [TD]") {
		t.Fatal("unexpected title match")
	}
}
