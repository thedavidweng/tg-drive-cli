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
