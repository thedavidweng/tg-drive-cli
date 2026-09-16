package service

import (
	"context"
	"strings"
	"testing"
	"time"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"github.com/thedavidweng/tg-drive-cli/core/telegram/fake"
)

// Saved-chat imports are tested at the service seam: the fake Telegram client
// models Saved Messages (sub-chats, forward headers, albums), the database is
// a real SQLite file, and every assertion is about observable state — the plan
// and result envelopes, the messages and records visible on Telegram, and the
// index.

func savedOriginHeader() *telegram.ForwardOrigin {
	return &telegram.ForwardOrigin{
		FromID: 777000,
		Title:  "Origin Channel",
		PostID: 4471,
		Date:   time.Date(2024, 3, 11, 10, 22, 0, 0, time.UTC),
	}
}

// seedSavedVideo adds a forwarded video saved into a sub-chat.
func seedSavedVideo(tg *fake.Client, id int, caption string, data []byte) telegram.Message {
	return tg.AddSavedMessage(telegram.Message{
		ID:       id,
		Kind:     telegram.KindDocument,
		FileName: "clip.mp4",
		MIME:     "video/mp4",
		FileSize: int64(len(data)),
		Data:     data,
		Caption:  caption,
		Video: &telegram.VideoAttributes{
			DurationSeconds:   42,
			Width:             1920,
			Height:            1080,
			SupportsStreaming: true,
		},
		Forward:        savedOriginHeader(),
		SavedPeerID:    5150,
		SavedPeerTitle: "Trips",
		Date:           time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC),
	})
}

func importItemByMessage(t *testing.T, res *ImportSavedResult, id int) ImportSavedItem {
	t.Helper()
	for _, it := range res.Items {
		if it.MessageID == id {
			return it
		}
	}
	t.Fatalf("no plan item for saved message %d: %+v", id, res.Items)
	return ImportSavedItem{}
}

func driveMessageByID(t *testing.T, app *App, ctx context.Context, id int) telegram.Message {
	t.Helper()
	tgChID, err := app.tgChannelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range app.TG.(*fake.Client).Messages(tgChID) {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("no drive message %d", id)
	return telegram.Message{}
}

func originRecords(t *testing.T, app *App, ctx context.Context) []manifest.OriginMeta {
	t.Helper()
	var out []manifest.OriginMeta
	for _, m := range machineRecords(t, app, ctx) {
		if !manifest.IsOriginRecord(m.Text) {
			continue
		}
		rec, err := manifest.ParseOriginRecord(m.Text)
		if err != nil {
			t.Fatalf("unparseable origin record %q: %v", m.Text, err)
		}
		out = append(out, rec)
	}
	return out
}

func dupeRecords(t *testing.T, app *App, ctx context.Context) []manifest.DupeMeta {
	t.Helper()
	var out []manifest.DupeMeta
	for _, m := range machineRecords(t, app, ctx) {
		if !manifest.IsDupeRecord(m.Text) {
			continue
		}
		rec, err := manifest.ParseDupeRecord(m.Text)
		if err != nil {
			t.Fatalf("unparseable dupe record %q: %v", m.Text, err)
		}
		out = append(out, rec)
	}
	return out
}

// A dry run classifies every saved item, mirrors sub-chats as directories,
// and touches nothing.
func TestImportSavedDryRunPlan(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	seedSavedVideo(tg, 101, "holiday clip", []byte("video-bytes"))
	tg.AddSavedMessage(telegram.Message{
		ID: 102, Kind: telegram.KindDocument, FileName: "report.pdf",
		MIME: "application/pdf", FileSize: 4, Data: []byte("pdf!"),
	})
	tg.AddSavedMessage(telegram.Message{ID: 103, Kind: telegram.KindText, Text: "remember this"})
	tg.AddSavedMessage(telegram.Message{ID: 104})

	var events []string
	res, err := app.ImportSaved(ctx, ImportSavedOptions{
		DryRun:   true,
		PhotosAs: PhotosAsDocument,
		Emit: func(event string, _ any) {
			events = append(events, event)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Into != DefaultImportSavedInto || !res.DryRun {
		t.Fatalf("unexpected envelope: %+v", res)
	}
	if res.Imported != 3 || res.Skipped != 1 {
		t.Fatalf("plan counts = %d imported, %d skipped: %+v", res.Imported, res.Skipped, res.Items)
	}
	if len(events) != 4 {
		t.Fatalf("dry-run events = %v, want one per item", events)
	}
	video := importItemByMessage(t, res, 101)
	if video.Kind != importKindVideo || video.Path != "/saved/Trips/clip.mp4" {
		t.Fatalf("video item = %+v", video)
	}
	if video.SubChat != "Trips" {
		t.Fatalf("sub-chat not mirrored: %+v", video)
	}
	if doc := importItemByMessage(t, res, 102); doc.Kind != importKindDocument || doc.Path != "/saved/report.pdf" {
		t.Fatalf("document item = %+v", doc)
	}
	if note := importItemByMessage(t, res, 103); note.Kind != importKindText || note.Path != "/saved/note-103.txt" {
		t.Fatalf("text item = %+v", note)
	}
	if empty := importItemByMessage(t, res, 104); empty.Action != "skip" || empty.Reason == "" {
		t.Fatalf("service message must be skipped with a reason: %+v", empty)
	}
	tgChID, _ := app.tgChannelID(ctx)
	if msgs := tg.Messages(tgChID); len(msgs) != 0 {
		t.Fatalf("dry run published %d messages", len(msgs))
	}
	if len(tg.SavedMessages()) != 4 {
		t.Fatal("dry run modified the saved chat")
	}
}

func TestDuplicateCaptionPreservationUsesBoundaries(t *testing.T) {
	existing := "file.bin\nMedia/Trips/\n\n#td_media"
	if duplicateCaptionAlreadyPreserved(existing, "Trips") {
		t.Fatal("parent path substring must not count as preserved caption")
	}
	if !duplicateCaptionAlreadyPreserved("Trips\n\nfile.bin", "Trips") {
		t.Fatal("caption prefix should count as preserved")
	}
	if !duplicateCaptionAlreadyPreserved("old\n---\nnew caption", "new caption") {
		t.Fatal("merged caption block should count as preserved")
	}
}

func TestImportSavedTextWritesNoteBytes(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tg.AddSavedMessage(telegram.Message{
		ID: 151, Kind: telegram.KindText, Text: "remember this note",
		Forward: savedOriginHeader(),
	})

	res, err := app.ImportSaved(ctx, ImportSavedOptions{PhotosAs: PhotosAsDocument})
	if err != nil {
		t.Fatal(err)
	}
	item := importItemByMessage(t, res, 151)
	if item.Action != "import" || item.Path != "/saved/note-151.txt" {
		t.Fatalf("item = %+v", item)
	}
	published := driveMessageByID(t, app, ctx, item.NewMessageID)
	if string(published.Data) != "remember this note" {
		t.Fatalf("note bytes = %q", published.Data)
	}
}

// Photos have no silent default: a non-interactive run without --photos-as
// fails with a usage error, and an interactive one is answered by the prompt.
func TestImportSavedPhotoChoiceRequired(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tg.AddSavedMessage(telegram.Message{ID: 201, Kind: telegram.KindPhoto, MIME: "image/jpeg", FileSize: 3, Data: []byte("jpg")})

	_, err := app.ImportSaved(ctx, ImportSavedOptions{DryRun: true})
	ae, ok := apperr.As(err)
	if !ok || ae.Code != apperr.ErrUsage {
		t.Fatalf("expected ERR_USAGE for a missing photo choice, got %v", err)
	}

	asked := 0
	res, err := app.ImportSaved(ctx, ImportSavedOptions{
		DryRun: true,
		PhotoPrompt: func(n int) (string, error) {
			asked = n
			return PhotosAsPhoto, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if asked != 1 || res.PhotosAs != PhotosAsPhoto {
		t.Fatalf("prompt not honored: asked=%d photos_as=%q", asked, res.PhotosAs)
	}
}

// A republished item carries fresh bytes, the original caption on top of the
// rendered block, the source video attributes, and a td-origin:v1 record.
func TestImportSavedRepublishesWithProvenance(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	seedSavedVideo(tg, 301, "第一次去北海道", []byte("video-bytes"))

	res, err := app.ImportSaved(ctx, ImportSavedOptions{PhotosAs: PhotosAsDocument})
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 1 {
		t.Fatalf("result = %+v", res)
	}
	item := importItemByMessage(t, res, 301)
	if item.NewMessageID == 0 || item.Hash == "" {
		t.Fatalf("item = %+v", item)
	}
	published := driveMessageByID(t, app, ctx, item.NewMessageID)
	if string(published.Data) != "video-bytes" {
		t.Fatalf("republished bytes = %q", published.Data)
	}
	if !strings.HasPrefix(published.Caption, "第一次去北海道") {
		t.Fatalf("original caption not preserved on top: %q", published.Caption)
	}
	if !strings.Contains(published.Caption, "clip.mp4") {
		t.Fatalf("rendered caption block missing: %q", published.Caption)
	}
	if published.Video == nil || published.Video.DurationSeconds != 42 ||
		published.Video.Width != 1920 || published.Video.Height != 1080 || !published.Video.SupportsStreaming {
		t.Fatalf("video attributes not carried over: %+v", published.Video)
	}

	records := originRecords(t, app, ctx)
	if len(records) != 1 {
		t.Fatalf("want one origin record, got %d", len(records))
	}
	rec := records[0]
	if rec.Source != manifest.SourceSaved || rec.SourceMsgID != 301 ||
		rec.OriginPostID != 4471 || rec.OriginTitle != "Origin Channel" || rec.OriginID != 777000 {
		t.Fatalf("origin record = %+v", rec)
	}
	if rec.CanonicalPath != item.Path || rec.ImportedAt == "" {
		t.Fatalf("origin record subject = %+v", rec)
	}
	// The index must hold the file like any other upload.
	entries, err := app.ListDir(ctx, "/saved/Trips")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Path != item.Path {
		t.Fatalf("index entries = %+v", entries)
	}
	if len(tg.SavedMessages()) != 1 {
		t.Fatal("the saved original must survive without --delete-source")
	}
}

// A saved album is republished as one td album: one native media group, one
// human caption on the first member, one td-album:v1 inventory.
func TestImportSavedAlbumStaysAnAlbum(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	members := []telegram.Message{
		{ID: 401, Kind: telegram.KindDocument, FileName: "a.bin", MIME: "application/octet-stream", FileSize: 2, Data: []byte("aa")},
		{ID: 402, Kind: telegram.KindDocument, FileName: "b.bin", MIME: "application/octet-stream", FileSize: 2, Data: []byte("bb")},
		{ID: 403, Kind: telegram.KindDocument, FileName: "c.bin", MIME: "application/octet-stream", FileSize: 2, Data: []byte("cc")},
	}
	tg.SeedSavedAlbum(members, 9001, savedOriginHeader(), 0, "", "album caption")

	res, err := app.ImportSaved(ctx, ImportSavedOptions{PhotosAs: PhotosAsDocument})
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 3 {
		t.Fatalf("result = %+v", res)
	}
	tgChID, _ := app.tgChannelID(ctx)
	grouped := groupedMembers(tg.Messages(tgChID))
	if len(grouped) != 3 {
		t.Fatalf("want 3 grouped members, got %d", len(grouped))
	}
	gid := grouped[0].GroupedID
	captions := 0
	for _, m := range grouped {
		if m.GroupedID != gid {
			t.Fatal("members must share one grouped id")
		}
		if m.Caption != "" {
			captions++
			if !strings.HasPrefix(m.Caption, "album caption") {
				t.Fatalf("album caption not preserved: %q", m.Caption)
			}
		}
	}
	if captions != 1 {
		t.Fatalf("want exactly one captioned member, got %d", captions)
	}
	if len(albumInventoryComments(t, app, ctx)) != 1 {
		t.Fatal("want exactly one td-album:v1 inventory")
	}
	records := originRecords(t, app, ctx)
	if len(records) != 1 || records[0].GroupedID == 0 || records[0].CanonicalPath != "" {
		t.Fatalf("one album must produce one group-scoped origin record: %+v", records)
	}
}

// Content already in the tree is not stored twice: the item is skipped as a
// duplicate and its caption survives in a td-dupe:v1 record.
func TestImportSavedDedupeRecordsSkippedCaption(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeNamedLocal(t, "shared.bin")
	if _, err := app.UploadFile(ctx, local, "/shared.bin", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	tg.AddSavedMessage(telegram.Message{
		ID: 501, Kind: telegram.KindDocument, FileName: "shared.bin",
		MIME: "application/octet-stream", FileSize: 20, Data: []byte("content of shared.bin"),
		Caption: "the caption only the saved copy has", Forward: savedOriginHeader(),
	})

	res, err := app.ImportSaved(ctx, ImportSavedOptions{PhotosAs: PhotosAsDocument})
	if err != nil {
		t.Fatal(err)
	}
	item := importItemByMessage(t, res, 501)
	if item.Action != "skip" || item.DuplicateOf != "/shared.bin" {
		t.Fatalf("item = %+v", item)
	}
	if res.Duplicates != 1 || res.Imported != 0 {
		t.Fatalf("result = %+v", res)
	}
	records := dupeRecords(t, app, ctx)
	if len(records) != 1 {
		t.Fatalf("want one dupe record, got %d", len(records))
	}
	if records[0].Caption != "the caption only the saved copy has" ||
		records[0].CanonicalPath != "/shared.bin" || records[0].SourceMsgID != 501 || records[0].Hash == "" {
		t.Fatalf("dupe record = %+v", records[0])
	}
	// Nothing new was uploaded.
	tgChID, _ := app.tgChannelID(ctx)
	media := 0
	for _, m := range tg.Messages(tgChID) {
		if m.Kind != "" && m.Text == "" {
			media++
		}
	}
	if media != 1 {
		t.Fatalf("duplicate was uploaded: %d media messages", media)
	}
}

// --merge-captions folds a duplicate's caption into the matched file, and a
// merge that cannot happen keeps the record and reports per item.
func TestImportSavedMergeCaptions(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeNamedLocal(t, "shared.bin")
	up, err := app.UploadFile(ctx, local, "/shared.bin", ConflictFail, false)
	if err != nil {
		t.Fatal(err)
	}
	tg.AddSavedMessage(telegram.Message{
		ID: 601, Kind: telegram.KindDocument, FileName: "shared.bin",
		MIME: "application/octet-stream", FileSize: 20, Data: []byte("content of shared.bin"),
		Caption: "richer text", Forward: savedOriginHeader(),
	})
	res, err := app.ImportSaved(ctx, ImportSavedOptions{PhotosAs: PhotosAsDocument, MergeCaptions: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.CaptionsMerged != 1 {
		t.Fatalf("result = %+v", res)
	}
	existing := driveMessageByID(t, app, ctx, up["message_id"].(int))
	if !strings.Contains(existing.Caption, manifest.MergeCaptionSeparator) || !strings.HasSuffix(existing.Caption, "richer text") {
		t.Fatalf("merged caption = %q", existing.Caption)
	}

	// A non-editable carrier must not fail the batch, and must keep a record.
	tg.SetNotEditable(mustTGChannel(t, app, ctx), up["message_id"].(int), true)
	tg.AddSavedMessage(telegram.Message{
		ID: 602, Kind: telegram.KindDocument, FileName: "shared.bin",
		MIME: "application/octet-stream", FileSize: 20, Data: []byte("content of shared.bin"),
		Caption: "another angle", Forward: savedOriginHeader(),
	})
	res, err = app.ImportSaved(ctx, ImportSavedOptions{PhotosAs: PhotosAsDocument, MergeCaptions: true, ContinueErr: true})
	if err != nil {
		t.Fatal(err)
	}
	item := importItemByMessage(t, res, 602)
	if item.Action != "skip" || item.CaptionMerged || item.Error == "" {
		t.Fatalf("non-editable merge must report per item: %+v", item)
	}
	found := false
	for _, rec := range dupeRecords(t, app, ctx) {
		if rec.SourceMsgID == 602 && rec.Caption == "another angle" {
			found = true
		}
	}
	if !found {
		t.Fatal("a failed merge must still record the duplicate's caption")
	}
}

func mustTGChannel(t *testing.T, app *App, ctx context.Context) int64 {
	t.Helper()
	id, err := app.tgChannelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Re-running an import stores nothing twice and adds no second record: the
// caption it would preserve is already in the tree.
func TestImportSavedIsIdempotent(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	seedSavedVideo(tg, 701, "same clip", []byte("video-bytes"))

	first, err := app.ImportSaved(ctx, ImportSavedOptions{PhotosAs: PhotosAsDocument})
	if err != nil {
		t.Fatal(err)
	}
	if first.Imported != 1 {
		t.Fatalf("first run = %+v", first)
	}
	second, err := app.ImportSaved(ctx, ImportSavedOptions{PhotosAs: PhotosAsDocument})
	if err != nil {
		t.Fatal(err)
	}
	if second.Imported != 0 || second.Duplicates != 1 {
		t.Fatalf("second run = %+v", second)
	}
	if recs := dupeRecords(t, app, ctx); len(recs) != 0 {
		t.Fatalf("a re-run must not record captions the tree already carries: %+v", recs)
	}
	entries, err := app.ListDir(ctx, "/saved/Trips")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("re-run stored the content twice: %+v", entries)
	}
}

// --delete-source removes the originals of published and duplicate items only
// after the drive copy is verified.
func TestImportSavedDeleteSource(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	seedSavedVideo(tg, 801, "clip", []byte("video-bytes"))
	local := writeNamedLocal(t, "shared.bin")
	if _, err := app.UploadFile(ctx, local, "/shared.bin", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	tg.AddSavedMessage(telegram.Message{
		ID: 802, Kind: telegram.KindDocument, FileName: "shared.bin",
		MIME: "application/octet-stream", FileSize: 20, Data: []byte("content of shared.bin"),
		Caption: "dupe caption", Forward: savedOriginHeader(),
	})

	res, err := app.ImportSaved(ctx, ImportSavedOptions{PhotosAs: PhotosAsDocument, DeleteSource: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.SourcesDeleted != 2 {
		t.Fatalf("result = %+v (items %+v)", res, res.Items)
	}
	if len(tg.SavedMessages()) != 0 {
		t.Fatalf("saved originals left behind: %+v", tg.SavedMessages())
	}
}

// A failed item never loses its source, and --continue-on-error keeps the
// rest of the batch moving.
func TestImportSavedKeepsSourcesOfFailures(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tg.AddSavedMessage(telegram.Message{
		ID: 901, Kind: telegram.KindDocument, FileName: "broken.bin",
		MIME: "application/octet-stream", FileSize: 3,
	})
	seedSavedVideo(tg, 902, "fine", []byte("video-bytes"))

	res, err := app.ImportSaved(ctx, ImportSavedOptions{
		PhotosAs: PhotosAsDocument, DeleteSource: true, ContinueErr: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 || res.Imported != 1 {
		t.Fatalf("result = %+v (items %+v)", res, res.Items)
	}
	remaining := tg.SavedMessages()
	if len(remaining) != 1 || remaining[0].ID != 901 {
		t.Fatalf("the failed item's source must survive: %+v", remaining)
	}
}

// Over-limit files are per-item failures, and without --continue-on-error the
// run stops before anything moves.
func TestImportSavedOverLimitFile(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tg.AddSavedMessage(telegram.Message{
		ID: 1001, Kind: telegram.KindDocument, FileName: "huge.bin",
		MIME: "application/octet-stream", FileSize: 8 * 1024 * 1024 * 1024,
	})

	_, err := app.ImportSaved(ctx, ImportSavedOptions{PhotosAs: PhotosAsDocument, DryRun: true})
	ae, ok := apperr.As(err)
	if !ok || ae.Code != apperr.ErrFileTooLarge {
		t.Fatalf("expected ERR_FILE_TOO_LARGE, got %v", err)
	}
	res, err := app.ImportSaved(ctx, ImportSavedOptions{PhotosAs: PhotosAsDocument, DryRun: true, ContinueErr: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 || importItemByMessage(t, res, 1001).Error == "" {
		t.Fatalf("result = %+v", res)
	}
}

// --into redirects the import, and --skip-existing leaves an occupied
// destination alone.
func TestImportSavedIntoAndConflictPolicy(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeNamedLocal(t, "clip.mp4")
	// The sub-chat directory is part of the destination, so the occupant has
	// to sit exactly where the import would land.
	if _, err := app.UploadFile(ctx, local, "/media/Trips/clip.mp4", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	seedSavedVideo(tg, 1101, "clip", []byte("different bytes"))

	res, err := app.ImportSaved(ctx, ImportSavedOptions{
		PhotosAs: PhotosAsDocument, Into: "/media", Policy: ConflictSkip, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	item := importItemByMessage(t, res, 1101)
	if item.Action != "skip" {
		t.Fatalf("item = %+v", item)
	}

	res, err = app.ImportSaved(ctx, ImportSavedOptions{
		PhotosAs: PhotosAsDocument, Into: "/media", Policy: ConflictRename, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	item = importItemByMessage(t, res, 1101)
	if item.Action != "import" || item.Path == "/media/Trips/clip.mp4" || !strings.HasPrefix(item.Path, "/media/Trips/") {
		t.Fatalf("auto-renamed item = %+v", item)
	}
}

// Progress events mirror the per-item results, so a long run can be watched
// without parsing the summary.
func TestImportSavedEmitsPerItemEvents(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	seedSavedVideo(tg, 1201, "clip", []byte("video-bytes"))

	var events []string
	if _, err := app.ImportSaved(ctx, ImportSavedOptions{
		PhotosAs: PhotosAsDocument,
		Emit:     func(event string, payload any) { events = append(events, event) },
	}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0] != "import.item" {
		t.Fatalf("events = %v", events)
	}
}
