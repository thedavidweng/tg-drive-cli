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

var tagSafe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_"

func assertSafeTags(t *testing.T, tags []string) {
	t.Helper()
	for _, tag := range tags {
		if !strings.HasPrefix(tag, "#td_") {
			t.Fatalf("tag %q does not start with #td_", tag)
		}
		for _, r := range tag[1:] {
			if !strings.ContainsRune(tagSafe, r) {
				t.Fatalf("tag %q contains unsafe rune %q", tag, r)
			}
		}
	}
}

func TestChainUsesSlugsNotRawSegments(t *testing.T) {
	tags, _, err := GenerateChain("/My Photos/2024/beach.jpg", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 {
		t.Fatalf("tags = %v", tags)
	}
	assertSafeTags(t, tags)
	if !strings.HasPrefix(tags[1], tags[0]+"_") {
		t.Fatalf("deep tag %q must extend shallow tag %q", tags[1], tags[0])
	}
}

func TestChainChineseSafe(t *testing.T) {
	tags, _, err := GenerateChain("/图片/2024/06/photo.jpg", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 3 {
		t.Fatalf("tags = %v", tags)
	}
	assertSafeTags(t, tags)
}

func TestChainEmojiSafe(t *testing.T) {
	tags, _, err := GenerateChain("/emoji/📷/x.jpg", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	assertSafeTags(t, tags)
}

func TestChainReusesExistingSlugs(t *testing.T) {
	existing := map[string]string{SlugKey("/", "Pictures"): "custom_abc12"}
	tags, mappings, err := GenerateChain("/Pictures/x.jpg", existing)
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 0 {
		t.Fatalf("expected no new mappings, got %v", mappings)
	}
	if tags[0] != "#td_custom_abc12" {
		t.Fatalf("tags = %v", tags)
	}
}

func TestChainCollisionExtendsHash(t *testing.T) {
	// Force a collision: a different segment in the same parent already
	// claimed the 5-char slug that "a b" would generate.
	fiveChar := SegmentSlug("a b", 5)
	existing := map[string]string{SlugKey("/", "other"): fiveChar}
	tags, mappings, err := GenerateChain("/a b/x.jpg", existing)
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 1 || mappings[0].HashLen != 8 {
		t.Fatalf("mappings = %v", mappings)
	}
	assertSafeTags(t, tags)
}

func TestChainCollisionAtEightFails(t *testing.T) {
	five := SegmentSlug("a b", 5)
	eight := SegmentSlug("a b", 8)
	existing := map[string]string{
		SlugKey("/", "seg1"): five,
		SlugKey("/", "seg2"): eight,
	}
	_, _, err := GenerateChain("/a b/x.jpg", existing)
	if err == nil {
		t.Fatal("expected slug collision error")
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
