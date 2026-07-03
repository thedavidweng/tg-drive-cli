package pathcodec

import (
	"strings"
	"testing"
)

func TestChineseSlug(t *testing.T) {
	slug := SegmentSlug("图片", 5)
	if !strings.Contains(slug, "_") {
		t.Fatalf("slug = %q", slug)
	}
}

func TestEmojiSlug(t *testing.T) {
	slug := SegmentSlug("😀test", 5)
	if slug == "" {
		t.Fatal("empty slug")
	}
}

func TestCollisionExtension(t *testing.T) {
	existing := map[string]string{}
	_, mappings, err := GenerateChain("/a/b/c.txt", existing)
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) < 2 {
		t.Fatalf("mappings = %d", len(mappings))
	}
}

func TestRepeatedNames(t *testing.T) {
	s1 := SegmentSlug("test", 5)
	s2 := SegmentSlug("test", 5)
	if s1 != s2 {
		t.Fatalf("%q != %q", s1, s2)
	}
}

func TestSpacesAndPunctuation(t *testing.T) {
	slug := SegmentSlug("hello world!", 5)
	if strings.Contains(slug, " ") {
		t.Fatalf("slug = %q", slug)
	}
}

func TestUnderscoreCollapse(t *testing.T) {
	slug := SegmentSlug("a__b", 5)
	if strings.Contains(slug, "__") {
		t.Fatalf("slug = %q", slug)
	}
}
