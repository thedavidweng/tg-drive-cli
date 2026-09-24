package pathcodec

import (
	"strings"
	"testing"
)

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

func TestChainCollisionExtendsHash(t *testing.T) {
	// Force a collision: a different segment in the same parent already
	// claimed the 8-char slug that "a b" would generate.
	eightChar := SegmentSlug("a b", 8)
	existing := map[string]string{SlugKey("/", "other"): eightChar}
	tags, mappings, err := GenerateChain("/a b/x.jpg", existing)
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 1 || mappings[0].HashLen != 13 {
		t.Fatalf("mappings = %v", mappings)
	}
	assertSafeTags(t, tags)
}

func TestChainCollisionAtTwentySixFails(t *testing.T) {
	eight := SegmentSlug("a b", 8)
	thirteen := SegmentSlug("a b", 13)
	twentySix := SegmentSlug("a b", 26)
	existing := map[string]string{
		SlugKey("/", "seg1"): eight,
		SlugKey("/", "seg2"): thirteen,
		SlugKey("/", "seg3"): twentySix,
	}
	_, _, err := GenerateChain("/a b/x.jpg", existing)
	if err == nil {
		t.Fatal("expected slug collision error")
	}
}
