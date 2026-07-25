package manifest

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/thedavidweng/tg-drive-cli/internal/apperr"
)

const (
	DefaultCaptionBudget = 1024
	DefaultTextBudget    = 4096
	DefaultMargin        = 16
)

// FileMeta holds metadata for caption/manifest rendering.
type FileMeta struct {
	CanonicalPath string
	DisplayName   string
	ParentHuman   string
	Size          int64
	Hash          string
	MIME          string
	Created       string
	Tags          []string
}

// UTF16Units counts UTF-16 code units per Telegram rules.
func UTF16Units(s string) int {
	if s == "" {
		return 0
	}
	runes := []rune(s)
	utf := utf16.Encode(runes)
	return len(utf)
}

// FitsTelegramCaption reports whether s fits the safe caption budget.
func FitsTelegramCaption(s string, budget, margin int) bool {
	if budget <= 0 {
		budget = DefaultCaptionBudget
	}
	if margin <= 0 {
		margin = DefaultMargin
	}
	return UTF16Units(s) <= budget-margin
}

// FitsTelegramText reports whether s fits the safe text budget.
func FitsTelegramText(s string, budget, margin int) bool {
	if budget <= 0 {
		budget = DefaultTextBudget
	}
	if margin <= 0 {
		margin = DefaultMargin
	}
	return UTF16Units(s) <= budget-margin
}

func b64(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func dashOr(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

// RenderCompact renders td:v1 compact metadata line.
func RenderCompact(m FileMeta) string {
	return fmt.Sprintf("td:v1 p=%s n=%s s=%d h=%s m=%s",
		b64(m.CanonicalPath), b64(m.DisplayName), m.Size, dashOr(m.Hash), dashOr(m.MIME))
}

// RenderManifestReply renders td-manifest:v1 full text.
func RenderManifestReply(m FileMeta) string {
	parent := strings.TrimSuffix(strings.TrimPrefix(m.CanonicalPath, "/"), "/"+m.DisplayName)
	if idx := strings.LastIndex(m.CanonicalPath, "/"); idx > 0 {
		parent = m.CanonicalPath[:idx]
	}
	lines := []string{
		"td-manifest:v1",
		fmt.Sprintf("p=%s", b64(m.CanonicalPath)),
		fmt.Sprintf("n=%s", b64(m.DisplayName)),
		fmt.Sprintf("parent=%s", b64(parent)),
		fmt.Sprintf("size=%d", m.Size),
		fmt.Sprintf("hash=%s", dashOr(m.Hash)),
		fmt.Sprintf("mime=%s", dashOr(m.MIME)),
	}
	if m.Created != "" {
		lines = append(lines, fmt.Sprintf("created=%s", m.Created))
	}
	if len(m.Tags) > 0 {
		lines = append(lines, fmt.Sprintf("tags=%s", strings.Join(m.Tags, " ")))
	}
	return strings.Join(lines, "\n")
}

// CaptionResult holds rendered caption and whether manifest reply is needed.
type CaptionResult struct {
	Caption            string
	NeedsManifestReply bool
	ManifestReply      string
	IncludedTags       []string
}

// RenderCaption builds media caption with hashtag chain within budget.
func RenderCaption(m FileMeta, budget, margin int) (CaptionResult, error) {
	if budget <= 0 {
		budget = DefaultCaptionBudget
	}
	if margin <= 0 {
		margin = DefaultMargin
	}
	limit := budget - margin

	lines := []string{m.DisplayName}
	if m.ParentHuman != "" {
		lines = append(lines, m.ParentHuman+"/")
	} else {
		lines = append(lines, "")
	}
	compact := RenderCompact(m)
	lines = append(lines, "", compact, "")

	var included []string
	for _, tag := range m.Tags {
		candidate := strings.TrimSpace(strings.Join(append(lines, tag), "\n"))
		if UTF16Units(candidate) > limit {
			break
		}
		lines = append(lines, tag)
		included = append(included, tag)
	}
	caption := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	allTagsIncluded := len(included) == len(m.Tags)
	if FitsTelegramCaption(caption, budget, margin) && allTagsIncluded {
		return CaptionResult{Caption: caption, IncludedTags: included}, nil
	}

	// Manifest reply fallback
	minLines := []string{m.DisplayName, m.ParentHuman + "/", "", "td:v1 manifest=reply", ""}
	var minTags []string
	for _, tag := range m.Tags {
		candidate := strings.TrimSpace(strings.Join(append(minLines, tag), "\n"))
		if UTF16Units(candidate) > limit {
			break
		}
		minLines = append(minLines, tag)
		minTags = append(minTags, tag)
	}
	minCaption := strings.TrimRight(strings.Join(minLines, "\n"), "\n")
	if !FitsTelegramCaption(minCaption, budget, margin) {
		return CaptionResult{}, apperr.New(apperr.ErrCaptionTooLong, "caption exceeds minimum budget")
	}
	replyMeta := m
	replyMeta.Tags = minTags
	return CaptionResult{
		Caption:            minCaption,
		NeedsManifestReply: true,
		ManifestReply:      RenderManifestReply(replyMeta),
		IncludedTags:       minTags,
	}, nil
}

var compactRe = regexp.MustCompile(`td:v1\s+(.+)`)
var kvRe = regexp.MustCompile(`(\w+)=([^\s]+)`)

// RenderTombstoneCaption renders the redacted media caption for a deleted file.
func RenderTombstoneCaption(displayName, canonicalPath string) string {
	return fmt.Sprintf("%s\n\ntd:v1 deleted=true p=%s", displayName, b64(canonicalPath))
}

// RenderTombstoneManifest renders the redacted manifest reply for a deleted file.
func RenderTombstoneManifest(canonicalPath string) string {
	return fmt.Sprintf("td-manifest:v1\ndeleted=true\np=%s", b64(canonicalPath))
}

// ParsedMeta is parsed td metadata.
type ParsedMeta struct {
	CanonicalPath string
	DisplayName   string
	Size          int64
	Hash          string
	MIME          string
	Parent        string
	Created       string
	Tags          []string
	ManifestReply bool
	Deleted       bool
}

func decodeB64(s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ParseCompact parses td:v1 compact metadata.
func ParseCompact(line string) (ParsedMeta, error) {
	m := compactRe.FindStringSubmatch(line)
	if len(m) < 2 {
		return ParsedMeta{}, apperr.New(apperr.ErrManifestInvalid, "missing td:v1")
	}
	if strings.Contains(m[1], "manifest=reply") {
		return ParsedMeta{ManifestReply: true}, nil
	}
	kvs := map[string]string{}
	for _, kv := range kvRe.FindAllStringSubmatch(m[1], -1) {
		kvs[kv[1]] = kv[2]
	}
	if kvs["deleted"] == "true" {
		meta := ParsedMeta{Deleted: true}
		if p, ok := kvs["p"]; ok {
			meta.CanonicalPath, _ = decodeB64(p)
		}
		return meta, nil
	}
	p, okP := kvs["p"]
	n, okN := kvs["n"]
	if !okP || !okN {
		return ParsedMeta{}, apperr.New(apperr.ErrManifestInvalid, "missing p or n")
	}
	cp, err := decodeB64(p)
	if err != nil {
		return ParsedMeta{}, apperr.New(apperr.ErrManifestInvalid, "invalid p")
	}
	dn, err := decodeB64(n)
	if err != nil {
		return ParsedMeta{}, apperr.New(apperr.ErrManifestInvalid, "invalid n")
	}
	meta := ParsedMeta{CanonicalPath: cp, DisplayName: dn}
	if v, ok := kvs["s"]; ok {
		_, _ = fmt.Sscanf(v, "%d", &meta.Size)
	}
	if v, ok := kvs["h"]; ok && v != "-" {
		meta.Hash = v
	}
	if v, ok := kvs["m"]; ok && v != "-" {
		meta.MIME = v
	}
	return meta, nil
}

// ParseManifestReply parses td-manifest:v1 text.
func ParseManifestReply(text string) (ParsedMeta, error) {
	if !strings.HasPrefix(text, "td-manifest:v1") {
		return ParsedMeta{}, apperr.New(apperr.ErrManifestInvalid, "missing td-manifest:v1")
	}
	kvs := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "td-manifest:") {
			continue
		}
		if i := strings.Index(line, "="); i > 0 {
			kvs[line[:i]] = line[i+1:]
		}
	}
	if kvs["deleted"] == "true" {
		meta := ParsedMeta{Deleted: true}
		if p, ok := kvs["p"]; ok {
			meta.CanonicalPath, _ = decodeB64(p)
		}
		return meta, nil
	}
	p, okP := kvs["p"]
	n, okN := kvs["n"]
	if !okP || !okN {
		return ParsedMeta{}, apperr.New(apperr.ErrManifestInvalid, "missing p or n")
	}
	cp, err := decodeB64(p)
	if err != nil {
		return ParsedMeta{}, apperr.New(apperr.ErrManifestInvalid, "invalid p")
	}
	dn, err := decodeB64(n)
	if err != nil {
		return ParsedMeta{}, apperr.New(apperr.ErrManifestInvalid, "invalid n")
	}
	meta := ParsedMeta{CanonicalPath: cp, DisplayName: dn}
	if v, ok := kvs["size"]; ok {
		_, _ = fmt.Sscanf(v, "%d", &meta.Size)
	}
	if v, ok := kvs["hash"]; ok && v != "-" {
		meta.Hash = v
	}
	if v, ok := kvs["mime"]; ok && v != "-" {
		meta.MIME = v
	}
	if v, ok := kvs["parent"]; ok {
		meta.Parent, _ = decodeB64(v)
	}
	if v, ok := kvs["created"]; ok {
		meta.Created = v
	}
	if v, ok := kvs["tags"]; ok {
		meta.Tags = strings.Fields(v)
	}
	return meta, nil
}

// ParseCaption extracts td:v1 from a full caption.
func ParseCaption(caption string) (ParsedMeta, error) {
	for _, line := range strings.Split(caption, "\n") {
		if strings.Contains(line, "td:v1") {
			return ParseCompact(strings.TrimSpace(line))
		}
	}
	return ParsedMeta{}, apperr.New(apperr.ErrManifestInvalid, "no td:v1 in caption")
}
