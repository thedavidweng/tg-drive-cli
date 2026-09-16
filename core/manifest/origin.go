package manifest

import (
	"fmt"
	"strconv"
	"strings"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
)

// Provenance records for imported content (td import saved).
//
// Both records are additive annotations posted as comments in the drive
// channel's discussion group (ADR 0018), alongside the frozen td-manifest:v1
// and td-album:v1 records, which they never modify. They are deliberately
// NOT authoritative: the virtual tree reconstructs from td-manifest:v1 and
// td-album:v1 alone, and losing an origin or dupe record costs provenance
// only, never a file or a path.
//
//   - td-origin:v1 says where a republished file (or album group) came from.
//   - td-dupe:v1 says that a saved item was skipped because its bytes already
//     lived in the tree, and preserves that item's caption, which would
//     otherwise die with the saved original.
const (
	OriginMagic = "td-origin:v1"
	DupeMagic   = "td-dupe:v1"
)

// SourceSaved is the import source recorded for Saved Messages content.
const SourceSaved = "saved"

// Origin is the shared provenance snapshot of one imported message. Titles
// and dates are snapshots on purpose: the origin channel can be deleted, and
// then this record is the only trace of where the bytes came from.
type Origin struct {
	// Source names the import source ("saved").
	Source string
	// SourceMsgID is the message id in the source chat.
	SourceMsgID int
	// OriginID and OriginTitle describe the chat the source message was
	// forwarded from; both are zero/empty for content saved directly.
	OriginID    int64
	OriginTitle string
	// OriginPostID is the origin channel's post id, 0 when there is none.
	OriginPostID int
	// ForwardDate is when the origin published the message (RFC 3339).
	ForwardDate string
	// ImportedAt is when td republished it (RFC 3339).
	ImportedAt string
}

// OriginMeta is one td-origin:v1 record. Single uploads name their canonical
// path; album groups name their grouped id instead, mirroring how
// td-album:v1 covers a whole group with one record.
type OriginMeta struct {
	Origin
	CanonicalPath string
	GroupedID     int64
}

// DupeMeta is one td-dupe:v1 record: a source item that was not imported
// because its content hash already matched CanonicalPath in the tree. The
// caption is recorded because it is the part of the skipped item that has no
// copy anywhere else.
type DupeMeta struct {
	Origin
	// CanonicalPath is the existing file whose bytes matched.
	CanonicalPath string
	// Hash is the matched content hash.
	Hash string
	// Caption is the skipped item's full caption.
	Caption string
	// CaptionTruncated reports that Caption did not fit the text budget and
	// was cut.
	CaptionTruncated bool
	// SourceDate is when the item was saved (RFC 3339).
	SourceDate string
}

// IsOriginRecord reports whether text is a td-origin:v1 record.
func IsOriginRecord(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), OriginMagic)
}

// IsDupeRecord reports whether text is a td-dupe:v1 record.
func IsDupeRecord(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), DupeMagic)
}

// originLines renders the fields shared by both record types.
func originLines(o Origin) []string {
	lines := []string{fmt.Sprintf("src=%s", dashOr(o.Source))}
	if o.SourceMsgID > 0 {
		lines = append(lines, fmt.Sprintf("smid=%d", o.SourceMsgID))
	}
	if o.OriginID != 0 {
		lines = append(lines, fmt.Sprintf("oid=%d", o.OriginID))
	}
	if o.OriginTitle != "" {
		lines = append(lines, fmt.Sprintf("otitle=%s", b64(o.OriginTitle)))
	}
	if o.OriginPostID > 0 {
		lines = append(lines, fmt.Sprintf("opid=%d", o.OriginPostID))
	}
	if o.ForwardDate != "" {
		lines = append(lines, fmt.Sprintf("fdate=%s", o.ForwardDate))
	}
	if o.ImportedAt != "" {
		lines = append(lines, fmt.Sprintf("idate=%s", o.ImportedAt))
	}
	return lines
}

// RenderOriginRecord renders a td-origin:v1 record.
func RenderOriginRecord(m OriginMeta) string {
	lines := []string{OriginMagic}
	if m.GroupedID != 0 {
		lines = append(lines, fmt.Sprintf("g=%d", m.GroupedID))
	}
	if m.CanonicalPath != "" {
		lines = append(lines, fmt.Sprintf("p=%s", b64(m.CanonicalPath)))
	}
	return strings.Join(append(lines, originLines(m.Origin)...), "\n")
}

// RenderDupeRecord renders a td-dupe:v1 record with the full caption.
func RenderDupeRecord(m DupeMeta) string {
	lines := []string{DupeMagic, fmt.Sprintf("p=%s", b64(m.CanonicalPath))}
	if m.Hash != "" {
		lines = append(lines, fmt.Sprintf("hash=%s", m.Hash))
	}
	if m.SourceDate != "" {
		lines = append(lines, fmt.Sprintf("sdate=%s", m.SourceDate))
	}
	lines = append(lines, originLines(m.Origin)...)
	if m.Caption != "" {
		lines = append(lines, fmt.Sprintf("cap=%s", b64(m.Caption)))
	}
	if m.CaptionTruncated {
		lines = append(lines, "capcut=1")
	}
	return strings.Join(lines, "\n")
}

// RenderDupeRecordFitting renders a td-dupe:v1 record inside the text budget.
// Only the recorded caption shrinks (and says so with capcut=1); the identity
// fields always survive, because a record that cannot name the matched file
// and hash records nothing.
func RenderDupeRecordFitting(m DupeMeta, budget, margin int) (string, error) {
	if budget <= 0 {
		budget = DefaultTextBudget
	}
	if margin <= 0 {
		margin = DefaultMargin
	}
	if full := RenderDupeRecord(m); FitsTelegramText(full, budget, margin) {
		return full, nil
	}
	bare := m
	bare.Caption = ""
	bare.CaptionTruncated = true
	if !FitsTelegramText(RenderDupeRecord(bare), budget, margin) {
		return "", apperr.New(apperr.ErrCaptionTooLong, "duplicate record exceeds Telegram text budget")
	}
	// Largest caption prefix that still fits. Cutting on runes keeps the
	// record valid UTF-8 even mid-grapheme.
	runes := []rune(m.Caption)
	lo, hi, best := 0, len(runes), RenderDupeRecord(bare)
	for lo <= hi {
		mid := (lo + hi) / 2
		cand := m
		cand.Caption = string(runes[:mid])
		cand.CaptionTruncated = mid < len(runes)
		rendered := RenderDupeRecord(cand)
		if FitsTelegramText(rendered, budget, margin) {
			best = rendered
			lo = mid + 1
			continue
		}
		hi = mid - 1
	}
	return best, nil
}

// parseRecordKVs reads the line-oriented key=value body of a record.
func parseRecordKVs(text, magic string) (map[string]string, error) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, magic) {
		return nil, apperr.New(apperr.ErrManifestInvalid, "missing "+magic)
	}
	kvs := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == magic {
			continue
		}
		if i := strings.Index(line, "="); i > 0 {
			kvs[line[:i]] = line[i+1:]
		}
	}
	return kvs, nil
}

// parseOriginFields reads the shared provenance fields.
func parseOriginFields(kvs map[string]string) (Origin, error) {
	o := Origin{}
	if v, ok := kvs["src"]; ok && v != "-" {
		o.Source = v
	}
	if v, ok := kvs["smid"]; ok {
		id, err := strconv.Atoi(v)
		if err != nil {
			return Origin{}, apperr.New(apperr.ErrManifestInvalid, "invalid source message id")
		}
		o.SourceMsgID = id
	}
	if v, ok := kvs["oid"]; ok {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return Origin{}, apperr.New(apperr.ErrManifestInvalid, "invalid origin id")
		}
		o.OriginID = id
	}
	if v, ok := kvs["otitle"]; ok {
		title, err := decodeB64(v)
		if err != nil {
			return Origin{}, apperr.New(apperr.ErrManifestInvalid, "invalid origin title")
		}
		o.OriginTitle = title
	}
	if v, ok := kvs["opid"]; ok {
		id, err := strconv.Atoi(v)
		if err != nil {
			return Origin{}, apperr.New(apperr.ErrManifestInvalid, "invalid origin post id")
		}
		o.OriginPostID = id
	}
	o.ForwardDate = kvs["fdate"]
	o.ImportedAt = kvs["idate"]
	return o, nil
}

// ParseOriginRecord parses a td-origin:v1 record.
func ParseOriginRecord(text string) (OriginMeta, error) {
	kvs, err := parseRecordKVs(text, OriginMagic)
	if err != nil {
		return OriginMeta{}, err
	}
	origin, err := parseOriginFields(kvs)
	if err != nil {
		return OriginMeta{}, err
	}
	out := OriginMeta{Origin: origin}
	if v, ok := kvs["p"]; ok {
		p, err := decodeB64(v)
		if err != nil {
			return OriginMeta{}, apperr.New(apperr.ErrManifestInvalid, "invalid path")
		}
		out.CanonicalPath = p
	}
	if v, ok := kvs["g"]; ok {
		g, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return OriginMeta{}, apperr.New(apperr.ErrManifestInvalid, "invalid grouped id")
		}
		out.GroupedID = g
	}
	if out.CanonicalPath == "" && out.GroupedID == 0 {
		return OriginMeta{}, apperr.New(apperr.ErrManifestInvalid, "origin record names neither a path nor a group")
	}
	return out, nil
}

// ParseDupeRecord parses a td-dupe:v1 record.
func ParseDupeRecord(text string) (DupeMeta, error) {
	kvs, err := parseRecordKVs(text, DupeMagic)
	if err != nil {
		return DupeMeta{}, err
	}
	origin, err := parseOriginFields(kvs)
	if err != nil {
		return DupeMeta{}, err
	}
	out := DupeMeta{Origin: origin, Hash: kvs["hash"], SourceDate: kvs["sdate"]}
	p, ok := kvs["p"]
	if !ok {
		return DupeMeta{}, apperr.New(apperr.ErrManifestInvalid, "duplicate record missing p")
	}
	path, err := decodeB64(p)
	if err != nil {
		return DupeMeta{}, apperr.New(apperr.ErrManifestInvalid, "invalid path")
	}
	out.CanonicalPath = path
	if v, ok := kvs["cap"]; ok {
		caption, err := decodeB64(v)
		if err != nil {
			return DupeMeta{}, apperr.New(apperr.ErrManifestInvalid, "invalid caption")
		}
		out.Caption = caption
	}
	out.CaptionTruncated = kvs["capcut"] == "1"
	return out, nil
}

// MergeCaptionSeparator divides an existing human caption from a duplicate's
// caption merged into it by td import saved --merge-captions.
const MergeCaptionSeparator = "---"

// MergeCaptions appends addition to existing, separated by a rule line, and
// reports whether the merge changed anything. A caption that already contains
// the addition is returned untouched, so repeated merges of the same
// duplicate cannot pile up.
func MergeCaptions(existing, addition string) (string, bool) {
	addition = strings.TrimSpace(addition)
	if addition == "" {
		return existing, false
	}
	trimmed := strings.TrimRight(existing, "\n")
	for _, block := range strings.Split(trimmed, "\n"+MergeCaptionSeparator+"\n") {
		if strings.TrimSpace(block) == addition {
			return existing, false
		}
	}
	if trimmed == "" {
		return addition, true
	}
	return trimmed + "\n" + MergeCaptionSeparator + "\n" + addition, true
}
