package manifest

import (
	"strings"
	"testing"
)

func sampleOrigin() Origin {
	return Origin{
		Source:       SourceSaved,
		SourceMsgID:  812,
		OriginID:     1234567890,
		OriginTitle:  "旅行 channel [gone]",
		OriginPostID: 4471,
		ForwardDate:  "2024-03-11T10:22:00Z",
		ImportedAt:   "2025-01-04T08:00:00Z",
	}
}

func TestOriginRecordRejectsRecordWithoutSubject(t *testing.T) {
	if _, err := ParseOriginRecord(OriginMagic + "\nsrc=saved\nsmid=1"); err == nil {
		t.Fatal("expected an error for a record naming neither a path nor a group")
	}
}

func TestOriginRecordRejectsForeignRecord(t *testing.T) {
	if _, err := ParseOriginRecord("td-manifest:v1\np=AA"); err == nil {
		t.Fatal("expected an error for a foreign record type")
	}
}

func TestDupeRecordRequiresMatchedPath(t *testing.T) {
	if _, err := ParseDupeRecord(DupeMagic + "\nsrc=saved\nhash=blake3:aa"); err == nil {
		t.Fatal("expected an error for a duplicate record without a matched path")
	}
}

// A caption at the top of Telegram's caption budget can still overflow the
// text budget once base64-encoded, so the record must shrink the caption
// instead of failing.
func TestDupeRecordFittingTruncatesOnlyTheCaption(t *testing.T) {
	in := DupeMeta{
		Origin:        sampleOrigin(),
		CanonicalPath: "/saved/trips/clip.mp4",
		Hash:          "blake3:deadbeef",
		Caption:       strings.Repeat("旅", 1024),
		SourceDate:    "2024-06-01T12:00:00Z",
	}
	text, err := RenderDupeRecordFitting(in, DefaultTextBudget, DefaultMargin)
	if err != nil {
		t.Fatal(err)
	}
	if !FitsTelegramText(text, DefaultTextBudget, DefaultMargin) {
		t.Fatalf("record does not fit the text budget: %d units", UTF16Units(text))
	}
	out, err := ParseDupeRecord(text)
	if err != nil {
		t.Fatal(err)
	}
	if !out.CaptionTruncated {
		t.Fatal("expected the truncation marker")
	}
	if out.CanonicalPath != in.CanonicalPath || out.Hash != in.Hash || out.SourceMsgID != in.SourceMsgID {
		t.Fatalf("identity fields lost: %+v", out)
	}
	if out.Caption == "" || !strings.HasPrefix(in.Caption, out.Caption) {
		t.Fatalf("truncated caption is not a prefix of the original: %q", out.Caption)
	}
}

func TestDupeRecordFittingKeepsShortCaptionsIntact(t *testing.T) {
	in := DupeMeta{Origin: sampleOrigin(), CanonicalPath: "/a.bin", Hash: "blake3:aa", Caption: "hello"}
	text, err := RenderDupeRecordFitting(in, DefaultTextBudget, DefaultMargin)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ParseDupeRecord(text)
	if err != nil {
		t.Fatal(err)
	}
	if out.Caption != "hello" || out.CaptionTruncated {
		t.Fatalf("short caption was altered: %+v", out)
	}
}

func TestProvenanceRecordsCountAsMachineMeta(t *testing.T) {
	if !HasMachineMeta(RenderOriginRecord(OriginMeta{Origin: sampleOrigin(), CanonicalPath: "/a"})) {
		t.Fatal("origin record must count as machine metadata")
	}
	if !HasMachineMeta(RenderDupeRecord(DupeMeta{Origin: sampleOrigin(), CanonicalPath: "/a"})) {
		t.Fatal("dupe record must count as machine metadata")
	}
	if HasMachineMeta("I mention td-origin:v1 mid-sentence in a caption") {
		t.Fatal("mid-sentence mentions must not count as machine metadata")
	}
}

// Album and manifest parsers must reject the new records, and vice versa, so
// a scan can classify comments by type without guessing.
func TestProvenanceRecordsDoNotParseAsFrozenRecords(t *testing.T) {
	origin := RenderOriginRecord(OriginMeta{Origin: sampleOrigin(), CanonicalPath: "/a"})
	if IsAlbumReply(origin) {
		t.Fatal("origin record must not read as an album inventory")
	}
	if _, err := ParseManifestReply(origin); err == nil {
		t.Fatal("origin record must not parse as a td-manifest:v1 record")
	}
	dupe := RenderDupeRecord(DupeMeta{Origin: sampleOrigin(), CanonicalPath: "/a"})
	if _, err := ParseAlbumReply(dupe); err == nil {
		t.Fatal("dupe record must not parse as an album inventory")
	}
}

func TestMergeCaptions(t *testing.T) {
	got, changed := MergeCaptions("first", "second")
	if !changed || got != "first\n"+MergeCaptionSeparator+"\nsecond" {
		t.Fatalf("merge = %q, changed = %v", got, changed)
	}
	// Repeating the same merge must be a no-op, so a retried import cannot
	// pile the same text up twice.
	again, changed := MergeCaptions(got, "second")
	if changed || again != got {
		t.Fatalf("repeat merge = %q, changed = %v", again, changed)
	}
	if _, changed := MergeCaptions("first", "  "); changed {
		t.Fatal("blank additions must not change the caption")
	}
	if got, changed := MergeCaptions("", "only"); !changed || got != "only" {
		t.Fatalf("merge into empty = %q, changed = %v", got, changed)
	}
}
