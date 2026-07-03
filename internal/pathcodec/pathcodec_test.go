package pathcodec

import (
	"regexp"
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

var hashtagAlnum = regexp.MustCompile(`^#td(_[A-Za-z0-9]+)+$`)

func TestChineseMultiLevelHashtagChain(t *testing.T) {
	existing := map[string]string{}
	tags, _, err := GenerateChain("/空 白/2024/file.jpg", existing)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 {
		t.Fatalf("tags = %v", tags)
	}
	for _, tag := range tags {
		if !hashtagAlnum.MatchString(tag) {
			t.Fatalf("invalid hashtag %q", tag)
		}
		if strings.Contains(tag, " ") {
			t.Fatalf("hashtag contains space: %q", tag)
		}
	}
}

func TestSlugChainAccumulates(t *testing.T) {
	existing := map[string]string{}
	tags, _, err := GenerateChain("/a/b/c.txt", existing)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 {
		t.Fatalf("tags = %v", tags)
	}
	if !strings.HasPrefix(tags[1], tags[0]+"_") {
		t.Fatalf("second tag %q should extend first %q", tags[1], tags[0])
	}
}
