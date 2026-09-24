package manifest

import (
	"strings"
	"testing"
)

func TestUTF16Units(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"hello", 5},
		{"你好", 2},
		{"😀", 2},
		{"a😀b", 4},
	}
	for _, tc := range cases {
		if got := UTF16Units(tc.s); got != tc.want {
			t.Errorf("UTF16Units(%q) = %d, want %d", tc.s, got, tc.want)
		}
	}
}

// Legacy carrier only (ADR 0018): deep paths overflow the caption budget,
// so the machine record falls back to a td-manifest:v1 reply.

// ADR 0018: captions are human-only; machine text never rides on them.

func TestTruncateUTF16KeepsRuneBoundaries(t *testing.T) {
	if got := TruncateUTF16("a😀bc", 4); got != "a😀…" {
		t.Fatalf("truncate = %q, want %q", got, "a😀…")
	}
	if got := TruncateUTF16("😀x", 2); got != "" {
		t.Fatalf("truncate without room = %q, want empty", got)
	}
	if got := TruncateUTF16("short", 20); got != "short" {
		t.Fatalf("unchanged string = %q", got)
	}
}

func TestManifestReplyFitsTextBudget(t *testing.T) {
	tags := make([]string, 40)
	for i := range tags {
		tags[i] = "#td_" + strings.Repeat("segxxxxxxxx", i+1)
	}
	m := FileMeta{
		DisplayName:   "f.bin",
		CanonicalPath: "/a/b/c/f.bin",
		Size:          1,
		Hash:          "blake3:" + strings.Repeat("ab", 32),
		MIME:          "application/octet-stream",
		Tags:          tags,
	}
	reply := RenderManifestReplyFitting(m, DefaultTextBudget, DefaultMargin)
	if !FitsTelegramText(reply, DefaultTextBudget, DefaultMargin) {
		t.Fatalf("reply units=%d", UTF16Units(reply))
	}
	if !strings.Contains(reply, "p=") || !strings.Contains(reply, "n=") {
		t.Fatalf("required fields missing: %s", reply)
	}
	if !strings.Contains(reply, tags[0]) {
		t.Fatal("shallow tag dropped")
	}
}

func TestCaptionTooLong(t *testing.T) {
	m := FileMeta{
		DisplayName: strings.Repeat("x", 2000),
		Tags:        []string{"#tag"},
	}
	_, err := RenderCaption(m, DefaultCaptionBudget, DefaultMargin)
	if err == nil {
		t.Fatal("expected ERR_CAPTION_TOO_LONG")
	}
}

func TestParseRejectsMissingP(t *testing.T) {
	_, err := ParseCompact("td:v1 n=abc")
	if err == nil {
		t.Fatal("expected error")
	}
}
