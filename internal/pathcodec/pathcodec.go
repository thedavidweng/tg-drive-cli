package pathcodec

import (
	"encoding/base32"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/mozillazg/go-pinyin"
	"github.com/mozillazg/go-unidecode"
	"github.com/thedavidweng/tg-drive-cli/internal/apperr"
	"golang.org/x/text/unicode/norm"
	"lukechampine.com/blake3"
)

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]+`)

// SegmentSlug generates a slug for one path segment.
func SegmentSlug(segment string, hashLen int) string {
	if hashLen <= 0 {
		hashLen = 5
	}
	seg := norm.NFC.String(segment)
	transliterated := transliterate(seg)
	slug := nonAlnum.ReplaceAllString(transliterated, "_")
	slug = strings.Trim(slug, "_")
	for strings.Contains(slug, "__") {
		slug = strings.ReplaceAll(slug, "__", "_")
	}
	if slug == "" {
		slug = "x"
	}
	if len(slug) > 24 {
		slug = slug[:24]
	}
	hash := blake3.Sum256([]byte(segment))
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	suffix := strings.ToLower(enc.EncodeToString(hash[:])[:hashLen])
	return slug + "_" + suffix
}

func transliterate(s string) string {
	if hasChinese(s) {
		args := pinyin.NewArgs()
		py := pinyin.Pinyin(s, args)
		var parts []string
		for _, p := range py {
			if len(p) > 0 {
				parts = append(parts, p[0])
			}
		}
		return strings.Join(parts, "")
	}
	return unidecode.Unidecode(s)
}

func hasChinese(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// SlugMapping stores segment slug info.
type SlugMapping struct {
	ParentCanonical string
	Segment         string
	Slug            string
	HashLen         int
}

// SlugKey builds the lookup key used by GenerateChain's existing map.
func SlugKey(parentCanonical, segment string) string {
	return parentCanonical + "|" + segment
}

// GenerateChain builds the shallow-to-deep hashtag chain for a canonical path.
// existing maps SlugKey(parent, segment) -> slug, preloaded from
// path_segment_slugs. Tag N is "#td_" + the first N segment slugs joined by
// "_", so every generated tag stays within the Telegram-safe charset.
func GenerateChain(canonical string, existing map[string]string) ([]string, []SlugMapping, error) {
	if canonical == "/" {
		return nil, nil, nil
	}
	parts := strings.Split(strings.TrimPrefix(canonical, "/"), "/")
	parent := "/"
	var tags []string
	var mappings []SlugMapping
	var slugChain []string
	for _, seg := range parts[:len(parts)-1] {
		key := SlugKey(parent, seg)
		slug, ok := existing[key]
		if !ok {
			taken := slugsInParent(existing, parent)
			hashLen := 5
			slug = SegmentSlug(seg, hashLen)
			if taken[slug] {
				hashLen = 8
				slug = SegmentSlug(seg, hashLen)
				if taken[slug] {
					return nil, nil, apperr.New(apperr.ErrSlugCollision, fmt.Sprintf("slug collision for segment %q under %s", seg, parent))
				}
			}
			mappings = append(mappings, SlugMapping{
				ParentCanonical: parent,
				Segment:         seg,
				Slug:            slug,
				HashLen:         hashLen,
			})
			existing[key] = slug
		}
		slugChain = append(slugChain, slug)
		tags = append(tags, "#td_"+strings.Join(slugChain, "_"))
		if parent == "/" {
			parent = "/" + seg
		} else {
			parent = parent + "/" + seg
		}
	}
	return tags, mappings, nil
}

// slugsInParent collects slugs already assigned to other segments of parent.
func slugsInParent(existing map[string]string, parent string) map[string]bool {
	out := map[string]bool{}
	prefix := parent + "|"
	for k, v := range existing {
		if strings.HasPrefix(k, prefix) {
			out[v] = true
		}
	}
	return out
}
