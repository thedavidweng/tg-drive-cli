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

// TagForSegment returns hashtag for a segment under parent slug chain.
func TagForSegment(parentSlugParts []string, segment string) string {
	slug := SegmentSlug(segment, 5)
	parts := append(append([]string(nil), parentSlugParts...), slug)
	return fmt.Sprintf("#td_%s", strings.Join(parts, "_"))
}

// SlugMapping stores segment slug info.
type SlugMapping struct {
	ParentCanonical string
	Segment         string
	Slug            string
	HashLen         int
}

// GenerateChain builds shallow-to-deep hashtag chain for a path.
// Parent levels use accumulated segment slugs, never raw path segments.
func GenerateChain(canonical string, existing map[string]string) ([]string, []SlugMapping, error) {
	if canonical == "/" {
		return nil, nil, nil
	}
	parts := strings.Split(strings.TrimPrefix(canonical, "/"), "/")
	parent := "/"
	var tags []string
	var mappings []SlugMapping
	var slugParts []string
	used := map[string]bool{}
	for _, seg := range parts[:len(parts)-1] {
		key := parent + "|" + seg
		slug, ok := existing[key]
		hashLen := 5
		if !ok {
			slug = SegmentSlug(seg, hashLen)
			for used[slug] {
				hashLen = 8
				slug = SegmentSlug(seg, hashLen)
				if hashLen > 8 {
					return nil, nil, apperr.New(apperr.ErrSlugCollision, "slug collision")
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
		used[slug] = true
		slugParts = append(slugParts, slug)
		tags = append(tags, fmt.Sprintf("#td_%s", strings.Join(slugParts, "_")))
		if parent == "/" {
			parent = "/" + seg
		} else {
			parent = parent + "/" + seg
		}
	}
	return tags, mappings, nil
}
