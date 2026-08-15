package manifest

import (
	"fmt"
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

func TestRenderAndParseCompact(t *testing.T) {
	m := FileMeta{
		CanonicalPath: "/Pictures/beach.jpg",
		DisplayName:   "beach.jpg",
		Size:          100,
		Hash:          "blake3:abc",
		MIME:          "image/jpeg",
	}
	line := RenderCompact(m)
	meta, err := ParseCompact(line)
	if err != nil {
		t.Fatal(err)
	}
	if meta.CanonicalPath != m.CanonicalPath || meta.DisplayName != m.DisplayName {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestDeepPathManifestReply(t *testing.T) {
	tags := make([]string, 50)
	for i := range tags {
		tags[i] = "#td_" + strings.Repeat("verylongtag", 6) + fmt.Sprintf("_%02d", i)
	}
	longParent := strings.Repeat("Vacation/Album/Photos/", 12)
	m := FileMeta{
		DisplayName:   "beach-vacation-sunset-photo-" + strings.Repeat("x", 60) + ".jpg",
		ParentHuman:   strings.TrimSuffix(longParent, "/"),
		CanonicalPath: "/" + longParent + "beach-vacation-sunset-photo.jpg",
		Size:          2482911,
		Hash:          "blake3:" + strings.Repeat("a", 64),
		MIME:          "image/jpeg",
		Tags:          tags,
	}
	res, err := RenderCaption(m, DefaultCaptionBudget, DefaultMargin)
	if err != nil {
		t.Fatal(err)
	}
	if !res.NeedsManifestReply {
		t.Fatalf("expected manifest reply, caption units=%d", UTF16Units(res.Caption))
	}
	if res.ManifestReply == "" {
		t.Fatal("missing manifest reply")
	}
	if !FitsTelegramText(res.ManifestReply, DefaultTextBudget, DefaultMargin) {
		t.Fatalf("reply exceeds text budget, units=%d", UTF16Units(res.ManifestReply))
	}
	if !strings.Contains(res.ManifestReply, tags[0]) {
		t.Fatal("shallow tag missing from manifest reply")
	}
	if !strings.Contains(res.ManifestReply, "parent=") {
		t.Fatal("manifest reply missing parent")
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

func TestHumanVisibleCaptionRestoresOriginal(t *testing.T) {
	polluted := "https://example.com\n#tag\n\nThe Bet.mp4\nvideos/\n\ntd:v1 p=x n=y\n#td_videos_abc"
	got := HumanVisibleCaption(polluted, "The Bet.mp4", "videos")
	if got != "https://example.com\n#tag" {
		t.Fatalf("got %q", got)
	}
	empty := "The Bet.mp4\nvideos/\n\ntd:v1 p=x n=y"
	if got := HumanVisibleCaption(empty, "The Bet.mp4", "videos"); got != "The Bet.mp4" {
		t.Fatalf("empty original -> filename, got %q", got)
	}
}

func TestSplitHumanAndMachine(t *testing.T) {
	s := "keep me\n\ntd:v1 p=abc n=def\n#td_x"
	if got := SplitHumanAndMachine(s); got != "keep me" {
		t.Fatalf("got %q", got)
	}
	if !HasMachineMeta(s) || HasMachineMeta("just text") {
		t.Fatal("HasMachineMeta")
	}
}

func TestManifestReplyParentForRootFile(t *testing.T) {
	m := FileMeta{
		DisplayName:   "beach.jpg",
		CanonicalPath: "/beach.jpg",
		Size:          1,
	}
	reply := RenderManifestReply(m)
	if !strings.Contains(reply, "parent="+b64("/")) {
		t.Fatalf("root parent missing, reply=%s", reply)
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

func TestAlbumReplyRoundTrip(t *testing.T) {
	m := AlbumMeta{
		GroupedID: 99,
		Files: []AlbumFile{
			{MessageID: 10, CanonicalPath: "/videos/a.mp4", DisplayName: "a.mp4", Size: 8, MIME: "video/mp4"},
			{MessageID: 11, CanonicalPath: "/videos/b.mp4", DisplayName: "b.mp4", Size: 9, Hash: "blake3:x", MIME: "video/mp4"},
		},
	}
	text := RenderAlbumReply(m)
	if !IsAlbumReply(text) || !HasMachineMeta(text) {
		t.Fatalf("magic missing: %s", text)
	}
	got, err := ParseAlbumReply(text)
	if err != nil {
		t.Fatal(err)
	}
	if got.GroupedID != 99 || len(got.Files) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got.Files[0].CanonicalPath != "/videos/a.mp4" || got.Files[1].Hash != "blake3:x" {
		t.Fatalf("files %+v", got.Files)
	}
	if SplitHumanAndMachine("#tag\n\n"+text) != "#tag" {
		t.Fatal("human prefix")
	}
}

func TestParseRejectsMissingP(t *testing.T) {
	_, err := ParseCompact("td:v1 n=abc")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestShallowTagsPreservedFirst(t *testing.T) {
	m := FileMeta{
		DisplayName:   "f.txt",
		ParentHuman:   "a/b",
		CanonicalPath: "/a/b/f.txt",
		Size:          1,
		Tags:          []string{"#shallow", "#deep_extra"},
	}
	res, err := RenderCaption(m, DefaultCaptionBudget, DefaultMargin)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.IncludedTags) == 0 || res.IncludedTags[0] != "#shallow" {
		t.Fatalf("tags = %v", res.IncludedTags)
	}
}
