package manifest

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
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

// TruncateUTF16 cuts s to at most limit UTF-16 code units, on a rune
// boundary, marking a cut with an ellipsis. It returns "" when not even the
// ellipsis fits, so callers can tell "shortened" from "no room at all".
func TruncateUTF16(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if UTF16Units(s) <= limit {
		return s
	}
	const ellipsis = "…"
	room := limit - UTF16Units(ellipsis)
	if room <= 0 {
		return ""
	}
	used, cut := 0, 0
	for i, r := range []rune(s) {
		n := UTF16Units(string(r))
		if used+n > room {
			break
		}
		used += n
		cut = i + 1
	}
	if cut == 0 {
		return ""
	}
	return string([]rune(s)[:cut]) + ellipsis
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
	parent := fsmodel.ParentPath(m.CanonicalPath)
	if parent == "" {
		parent = "/"
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

// RenderManifestReplyFitting keeps required metadata and as many shallow
// tags as fit in the text-message budget. Deep tags are dropped first.
func RenderManifestReplyFitting(m FileMeta, budget, margin int) string {
	if budget <= 0 {
		budget = DefaultTextBudget
	}
	if margin <= 0 {
		margin = DefaultMargin
	}
	limit := budget - margin
	tags := append([]string(nil), m.Tags...)
	for {
		candidate := m
		candidate.Tags = tags
		reply := RenderManifestReply(candidate)
		if UTF16Units(reply) <= limit || len(tags) == 0 {
			return reply
		}
		tags = tags[:len(tags)-1]
	}
}

// CaptionResult holds the rendered caption. Modern captions no longer carry
// path-derived tags, so IncludedTags is empty for RenderCaption and
// RenderCaptionWithPrefix; it remains for compatibility with callers that
// inspect the result.
type CaptionResult struct {
	Caption      string
	IncludedTags []string
}

// RenderCaption builds the human media caption. Machine metadata and
// path-derived browsing scaffolding never ride on modern captions; they live
// in the discussion-thread manifest (ADR 0018).
func RenderCaption(m FileMeta, budget, margin int) (CaptionResult, error) {
	return RenderCaptionWithPrefix(m, "", budget, margin)
}

// RenderCaptionWithPrefix renders the caption with an imported message's own
// text above the display name, separated by a blank line. Imports need it:
// the source caption is content in its own right and must stay visible on the
// republished message. A prefix too long for the budget is truncated rather
// than dropped, and never displaces the display name.
func RenderCaptionWithPrefix(m FileMeta, prefix string, budget, margin int) (CaptionResult, error) {
	if budget <= 0 {
		budget = DefaultCaptionBudget
	}
	if margin <= 0 {
		margin = DefaultMargin
	}
	limit := budget - margin

	block := []string{m.DisplayName}

	var lines []string
	if prefix = strings.TrimRight(prefix, "\n"); prefix != "" {
		// +2 for the blank line that separates the prefix from the block.
		room := limit - UTF16Units(strings.Join(block, "\n")) - 2
		prefix = TruncateUTF16(prefix, room)
		if prefix != "" {
			lines = append(lines, prefix, "")
		}
	}
	lines = append(lines, block...)
	caption := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	if !FitsTelegramCaption(caption, budget, margin) {
		return CaptionResult{}, apperr.New(apperr.ErrCaptionTooLong, "caption exceeds minimum budget")
	}
	return CaptionResult{Caption: caption}, nil
}

// RenderLegacyCaption renders the pre-ADR-0018 caption: human text plus the
// td:v1 compact machine line, falling back to a minimal caption plus a full
// td-manifest:v1 reply text when the record does not fit. It exists only so
// rows published with the legacy in-channel carriers can be edited (mv,
// repair) without losing their machine record; new uploads never use it.
func RenderLegacyCaption(m FileMeta, budget, margin int) (caption, manifestReply string, needsReply bool, err error) {
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

	// UTF-16 length is additive over concatenation, so the running count
	// tracks each appended tag instead of re-encoding the whole caption.
	cur := UTF16Units(strings.Join(lines, "\n"))
	var included []string
	for _, tag := range m.Tags {
		add := UTF16Units("\n" + tag)
		if cur+add > limit {
			break
		}
		lines = append(lines, tag)
		included = append(included, tag)
		cur += add
	}
	caption = strings.TrimRight(strings.Join(lines, "\n"), "\n")
	allTagsIncluded := len(included) == len(m.Tags)
	if FitsTelegramCaption(caption, budget, margin) && allTagsIncluded {
		return caption, "", false, nil
	}

	// Manifest reply fallback
	minLines := []string{m.DisplayName, m.ParentHuman + "/", "", "td:v1 manifest=reply", ""}
	minCur := UTF16Units(strings.Join(minLines, "\n"))
	for _, tag := range m.Tags {
		add := UTF16Units("\n" + tag)
		if minCur+add > limit {
			break
		}
		minLines = append(minLines, tag)
		minCur += add
	}
	minCaption := strings.TrimRight(strings.Join(minLines, "\n"), "\n")
	if !FitsTelegramCaption(minCaption, budget, margin) {
		return "", "", false, apperr.New(apperr.ErrCaptionTooLong, "caption exceeds minimum budget")
	}
	return minCaption, RenderManifestReplyFitting(m, DefaultTextBudget, margin), true, nil
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

// SplitHumanAndMachine returns the human-visible prefix before any td:v1 /
// td-manifest / td-album line. Used when adopting a message that already has a caption.
func SplitHumanAndMachine(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	cut := len(lines)
	for i, line := range lines {
		if lineHasMachineMeta(line) {
			cut = i
			break
		}
	}
	return strings.TrimRight(strings.Join(lines[:cut], "\n"), "\n")
}

// lineHasMachineMeta reports whether one trimmed line is a machine metadata
// record: a line that STARTS with the td:v1 / td-manifest:v1 / td-album:v1 /
// td-origin:v1 / td-dupe:v1 marker. Line-anchoring matters: a human caption
// that merely mentions "td:v1" mid-sentence must not be treated as managed
// metadata.
func lineHasMachineMeta(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "td:v1") ||
		strings.HasPrefix(t, "td-manifest:v1") ||
		strings.HasPrefix(t, AlbumMagic) ||
		strings.HasPrefix(t, OriginMagic) ||
		strings.HasPrefix(t, DupeMagic)
}

// HasMachineMeta reports whether s contains a machine metadata line.
func HasMachineMeta(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		if lineHasMachineMeta(line) {
			return true
		}
	}
	return false
}

// StripAdoptScaffold removes the filename + parent/ lines import used to
// inject above td:v1, leaving only the original human caption.
func StripAdoptScaffold(human, display, parentHuman string) string {
	human = strings.TrimRight(human, "\n")
	if display == "" {
		return human
	}
	if parentHuman != "" {
		scaffold := display + "\n" + parentHuman + "/"
		if human == scaffold {
			return ""
		}
		for _, sep := range []string{"\n\n" + scaffold, "\n" + scaffold} {
			if strings.HasSuffix(human, sep) {
				return strings.TrimRight(strings.TrimSuffix(human, sep), "\n")
			}
		}
	}
	return human
}

// StripRenderedScaffold removes the legacy modern-caption path scaffolding
// from a caption while preserving any imported or user-authored prefix.
// Only an exact suffix made from the indexed display name, parent line, and
// stored path-tag prefix is removed. This keeps human hashtags and text that
// merely resemble the scaffold intact.
func StripRenderedScaffold(caption, display, parentHuman string, tags []string) (string, bool) {
	caption = strings.TrimRight(caption, "\n")
	if caption == "" || display == "" {
		return caption, false
	}

	for count := len(tags); count >= 0; count-- {
		lines := []string{display}
		if parentHuman != "" {
			lines = append(lines, parentHuman+"/")
		} else {
			lines = append(lines, "")
		}
		if count > 0 {
			lines = append(lines, "")
			lines = append(lines, tags[:count]...)
		}
		suffix := strings.Join(lines, "\n")
		if caption == suffix {
			clean := display
			return clean, clean != caption
		}
		separator := "\n\n" + suffix
		if strings.HasSuffix(caption, separator) {
			prefix := strings.TrimRight(strings.TrimSuffix(caption, separator), "\n")
			if prefix != "" {
				return prefix, true
			}
			return display, true
		}
	}
	return caption, false
}

// HumanVisibleCaption is what should remain on the Telegram media/text
// message: the original human caption, or the filename if there was none.
func HumanVisibleCaption(body, display, parentHuman string) string {
	human := StripAdoptScaffold(SplitHumanAndMachine(body), display, parentHuman)
	if strings.TrimSpace(human) != "" {
		return human
	}
	return display
}

// ParseCaption extracts td:v1 from a full caption.
func ParseCaption(caption string) (ParsedMeta, error) {
	for _, line := range strings.Split(caption, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "td:v1") {
			return ParseCompact(strings.TrimSpace(line))
		}
	}
	return ParsedMeta{}, apperr.New(apperr.ErrManifestInvalid, "no td:v1 in caption")
}
