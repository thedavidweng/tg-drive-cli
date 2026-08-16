package service

import (
	"context"
	"strings"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

func TestImportDryRunDoesNotEdit(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)
	tg.AddMessage(tgChID, telegram.Message{
		ID: 50, Kind: telegram.KindDocument, MIME: "video/mp4",
		FileName: "The Bet.mp4", FileSize: 1000, Data: []byte("video"),
	})
	res, err := app.Import(ctx, ImportOptions{Unmanaged: true, DryRun: true, NoHash: true})
	if err != nil {
		t.Fatal(err)
	}
	var adopted int
	for _, it := range res.Items {
		if it.Action == "import" {
			adopted++
		}
	}
	if adopted != 1 {
		t.Fatalf("imported=%d items=%v", adopted, res.Items)
	}
	if res.Items[0].Path != "/videos/The Bet.mp4" {
		t.Fatalf("path = %q", res.Items[0].Path)
	}
	got, err := tg.GetMessage(ctx, tgChID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got.Caption != "" {
		t.Fatalf("dry-run edited caption: %q", got.Caption)
	}
}

func TestImportAdoptsVideoPhotoAndText(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)
	tg.AddMessage(tgChID, telegram.Message{
		ID: 70, Kind: telegram.KindDocument, MIME: "video/mp4",
		FileName: "clip.mp4", FileSize: 8, Data: []byte("videodata"),
	})
	tg.AddMessage(tgChID, telegram.Message{
		ID: 71, Kind: telegram.KindPhoto, MIME: "image/jpeg",
		FileName: "photo-71.jpg", Data: []byte("jpegdata"),
	})
	tg.AddMessage(tgChID, telegram.Message{
		ID: 72, Kind: telegram.KindText, MIME: "text/plain", Text: "shopping list\nmilk",
	})
	res, err := app.Import(ctx, ImportOptions{Unmanaged: true, NoHash: true})
	if err != nil {
		t.Fatal(err)
	}
	var adopted int
	for _, it := range res.Items {
		if it.Action == "import" {
			adopted++
		}
	}
	if adopted != 3 {
		t.Fatalf("adopted=%d items=%+v", adopted, res.Items)
	}
	for _, p := range []string{"/videos/clip.mp4", "/photos/photo-71.jpg", "/notes/shopping list.txt"} {
		if got := fileStatus(t, app, p); got != "active" {
			t.Fatalf("%s status=%q", p, got)
		}
	}
	v, err := tg.GetMessage(ctx, tgChID, 70)
	if err != nil {
		t.Fatal(err)
	}
	if v.Caption != "" {
		t.Fatalf("import must not invent a caption: %q", v.Caption)
	}
	note, err := tg.GetMessage(ctx, tgChID, 72)
	if err != nil {
		t.Fatal(err)
	}
	if note.Text != "shopping list\nmilk" {
		t.Fatalf("text should stay untouched: %+v", note)
	}
	var replyCount int
	for _, m := range tg.Messages(tgChID) {
		if m.ReplyTo != nil && strings.Contains(m.Text, "td-manifest:v1") {
			replyCount++
		}
	}
	if replyCount != 3 {
		t.Fatalf("ungrouped files should each have one reconstructable reply, got %d", replyCount)
	}
}

func TestRewriteCaptionsRestoresHumanText(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)
	tg.AddMessage(tgChID, telegram.Message{
		ID: 80, Kind: telegram.KindDocument, MIME: "video/mp4",
		FileName: "clip.mp4", FileSize: 4, Data: []byte("abcd"),
		Caption: "https://example.com\n#tag",
	})
	if _, err := app.Import(ctx, ImportOptions{MessageID: 80, Dest: "/videos/clip.mp4", NoHash: true}); err != nil {
		t.Fatal(err)
	}
	// Simulate the old bug: machine metadata overwritten onto the media.
	if err := tg.EditCaption(ctx, tgChID, 80, "https://example.com\n#tag\n\nclip.mp4\nvideos/\n\ntd:v1 p=x n=y\n#td_videos_xx"); err != nil {
		t.Fatal(err)
	}
	res, err := app.Import(ctx, ImportOptions{RewriteCaptions: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported < 1 {
		t.Fatalf("res = %+v", res)
	}
	got, err := tg.GetMessage(ctx, tgChID, 80)
	if err != nil {
		t.Fatal(err)
	}
	if got.Caption != "https://example.com\n#tag" {
		t.Fatalf("restored caption = %q", got.Caption)
	}
	if strings.Contains(got.Caption, "td:v1") {
		t.Fatal("machine meta still on media")
	}
}

func TestImportSkipsManagedAndManifestReply(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "x")
	if _, err := app.UploadFile(ctx, local, "/already.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	res, err := app.Import(ctx, ImportOptions{Unmanaged: true, DryRun: true, NoHash: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 0 {
		t.Fatalf("should skip managed uploads, got %+v", res)
	}
}

func TestAlbumCaptionPrefersHashtagsOverFilenames(t *testing.T) {
	members := []telegram.Message{
		{ID: 1, FileName: "a.mp4", Caption: "a.mp4"},
		{ID: 2, FileName: "b.mp4", Caption: "#tag\nhello"},
		{ID: 3, FileName: "c.mp4", Caption: "c.mp4"},
	}
	got := pickAlbumCaption(members, nil)
	if got != "#tag\nhello" {
		t.Fatalf("got %q", got)
	}
	if !isInventedCaption("The Bet.mp4", "The Bet.mp4", "The Bet.mp4") {
		t.Fatal("filename should be invented")
	}
	if isInventedCaption("#clarabelles", "x.mp4", "x.mp4") {
		t.Fatal("hashtag caption is human")
	}
	if albumCaptionCandidate("抖音 dump #Aqr_o2_.jpg", "photo-3.jpg", "抖音 dump #Aqr_o2_.jpg") != "抖音 dump #Aqr_o2_" {
		t.Fatal("should recover human text from generated filename")
	}
}

func TestProposeImportPathSanitizes(t *testing.T) {
	p := proposeImportPath(telegram.Message{ID: 1, Kind: telegram.KindDocument, MIME: "video/mp4", FileName: "a/b.mp4"}, "/")
	if p != "/videos/a_b.mp4" {
		t.Fatalf("path = %q", p)
	}
}

func TestImportLeavesAlbumCaptionAlone(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)
	tg.AddMessage(tgChID, telegram.Message{
		ID: 200, Kind: telegram.KindDocument, MIME: "video/mp4",
		FileName: "a.mp4", FileSize: 4, Data: []byte("aaaa"),
		Caption: "#clarabelles\nweekend dump", GroupedID: 7,
	})
	tg.AddMessage(tgChID, telegram.Message{
		ID: 201, Kind: telegram.KindDocument, MIME: "video/mp4",
		FileName: "b.mp4", FileSize: 4, Data: []byte("bbbb"),
		GroupedID: 7,
	})
	tg.AddMessage(tgChID, telegram.Message{
		ID: 202, Kind: telegram.KindDocument, MIME: "video/mp4",
		FileName: "c.mp4", FileSize: 4, Data: []byte("cccc"),
		GroupedID: 7,
	})
	if _, err := app.Import(ctx, ImportOptions{Unmanaged: true, NoHash: true}); err != nil {
		t.Fatal(err)
	}
	first, _ := tg.GetMessage(ctx, tgChID, 200)
	if first.Caption != "#clarabelles\nweekend dump" {
		t.Fatalf("album caption changed: %q", first.Caption)
	}
	for _, id := range []int{201, 202} {
		got, _ := tg.GetMessage(ctx, tgChID, id)
		if got.Caption != "" {
			t.Fatalf("sibling %d caption = %q", id, got.Caption)
		}
	}
	var albumReplies, fileReplies int
	for _, m := range tg.Messages(tgChID) {
		if strings.Contains(m.Text, "td-album:v1") {
			albumReplies++
		}
		if strings.HasPrefix(strings.TrimSpace(m.Text), "td-manifest:v1") {
			fileReplies++
		}
	}
	if albumReplies != 1 {
		t.Fatalf("album replies = %d, want 1", albumReplies)
	}
	if fileReplies != 0 {
		t.Fatalf("per-file replies = %d, want 0", fileReplies)
	}
}

func TestRewriteAlbumRestoresGroupCaptionAndDeletesReplies(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)
	tg.AddMessage(tgChID, telegram.Message{
		ID: 300, Kind: telegram.KindDocument, MIME: "video/mp4",
		FileName: "a.mp4", FileSize: 4, Data: []byte("aaaa"),
		Caption: "a.mp4", GroupedID: 9,
	})
	tg.AddMessage(tgChID, telegram.Message{
		ID: 301, Kind: telegram.KindDocument, MIME: "video/mp4",
		FileName: "b.mp4", FileSize: 4, Data: []byte("bbbb"),
		Caption: "#CreamBerryFairy\nxhamster dump", GroupedID: 9,
	})
	tg.AddMessage(tgChID, telegram.Message{
		ID: 302, Kind: telegram.KindDocument, MIME: "video/mp4",
		FileName: "c.mp4", FileSize: 4, Data: []byte("cccc"),
		Caption: "c.mp4", GroupedID: 9,
	})
	if _, err := app.Import(ctx, ImportOptions{Unmanaged: true, NoHash: true}); err != nil {
		t.Fatal(err)
	}
	rt := 300
	tg.AddMessage(tgChID, telegram.Message{ID: 400, Text: "td-manifest:v1 p=x", ReplyTo: &rt})
	rt2 := 301
	tg.AddMessage(tgChID, telegram.Message{ID: 401, Text: "td-manifest:v1 p=y", ReplyTo: &rt2})

	res, err := app.Import(ctx, ImportOptions{RewriteCaptions: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 2 {
		t.Fatalf("deleted=%d items=%+v", res.Deleted, res.Items)
	}
	first, _ := tg.GetMessage(ctx, tgChID, 300)
	if first.Caption != "#CreamBerryFairy\nxhamster dump" {
		t.Fatalf("first caption = %q", first.Caption)
	}
	for _, id := range []int{301, 302} {
		got, _ := tg.GetMessage(ctx, tgChID, id)
		if got.Caption != "" {
			t.Fatalf("sibling %d still has caption %q", id, got.Caption)
		}
	}
	var albumReplies, fileReplies int
	for _, m := range tg.Messages(tgChID) {
		if strings.Contains(m.Text, "td-album:v1") {
			albumReplies++
		}
		if strings.HasPrefix(strings.TrimSpace(m.Text), "td-manifest:v1") {
			fileReplies++
		}
	}
	if fileReplies != 0 {
		t.Fatalf("per-file replies left: %d", fileReplies)
	}
	if albumReplies != 1 {
		t.Fatalf("album replies = %d, want 1", albumReplies)
	}
}

func TestScanRebuildsAlbumFromReply(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)
	tg.AddMessage(tgChID, telegram.Message{
		ID: 600, Kind: telegram.KindDocument, MIME: "video/mp4",
		FileName: "a.mp4", FileSize: 4, Data: []byte("aaaa"),
		Caption: "#theme", GroupedID: 12,
	})
	tg.AddMessage(tgChID, telegram.Message{
		ID: 601, Kind: telegram.KindDocument, MIME: "video/mp4",
		FileName: "b.mp4", FileSize: 4, Data: []byte("bbbb"),
		GroupedID: 12,
	})
	if _, err := app.Import(ctx, ImportOptions{Unmanaged: true, NoHash: true}); err != nil {
		t.Fatal(err)
	}
	channelID, _, _ := app.channelID(ctx)
	for _, table := range []string{"path_tags", "path_segment_slugs", "files", "nodes", "scan_state"} {
		if _, err := app.DB.Raw().Exec(`delete from ` + table); err != nil {
			t.Fatal(err)
		}
	}
	res, err := app.Scan(ctx, ScanOptions{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if res["active"].(int) != 2 {
		t.Fatalf("active=%v items after rebuild", res["active"])
	}
	_ = channelID
	if got := fileStatus(t, app, "/videos/a.mp4"); got != "active" {
		t.Fatalf("a.mp4 status=%q", got)
	}
	if got := fileStatus(t, app, "/videos/b.mp4"); got != "active" {
		t.Fatalf("b.mp4 status=%q", got)
	}
	first, _ := tg.GetMessage(ctx, tgChID, 600)
	if first.Caption != "#theme" {
		t.Fatalf("human caption damaged by scan: %q", first.Caption)
	}
}

func TestMoveAndDeleteAlbumMemberKeepsGroup(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)
	tg.AddMessage(tgChID, telegram.Message{
		ID: 700, Kind: telegram.KindDocument, MIME: "video/mp4",
		FileName: "a.mp4", FileSize: 4, Data: []byte("aaaa"),
		Caption: "#theme", GroupedID: 15,
	})
	tg.AddMessage(tgChID, telegram.Message{
		ID: 701, Kind: telegram.KindDocument, MIME: "video/mp4",
		FileName: "b.mp4", FileSize: 4, Data: []byte("bbbb"),
		GroupedID: 15,
	})
	if _, err := app.Import(ctx, ImportOptions{Unmanaged: true, NoHash: true}); err != nil {
		t.Fatal(err)
	}
	if err := app.MoveFile(ctx, "/videos/b.mp4", "/clips/b.mp4"); err != nil {
		t.Fatal(err)
	}
	first, _ := tg.GetMessage(ctx, tgChID, 700)
	if first.Caption != "#theme" {
		t.Fatalf("move edited album caption: %q", first.Caption)
	}
	sib, _ := tg.GetMessage(ctx, tgChID, 701)
	if sib.Caption != "" {
		t.Fatalf("move stamped sibling: %q", sib.Caption)
	}
	if got := fileStatus(t, app, "/clips/b.mp4"); got != "active" {
		t.Fatalf("moved status=%q", got)
	}
	if _, err := app.DeleteFile(ctx, "/videos/a.mp4", DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	var albumReplies int
	for _, m := range tg.Messages(tgChID) {
		if strings.Contains(m.Text, "td-album:v1") {
			albumReplies++
			got, err := manifest.ParseAlbumReply(m.Text)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Files) != 1 || got.Files[0].CanonicalPath != "/clips/b.mp4" {
				t.Fatalf("album after delete: %+v", got)
			}
		}
		if m.ID == 700 {
			t.Fatal("deleted album member still on telegram")
		}
	}
	if albumReplies != 1 {
		t.Fatalf("album replies after delete = %d", albumReplies)
	}
	if got := fileStatus(t, app, "/clips/b.mp4"); got != "active" {
		t.Fatalf("remaining member status=%q", got)
	}
}

func TestScanKeepsAdoptedFilesWithoutMetadata(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	tgChID, _ := app.tgChannelID(ctx)
	tg.AddMessage(tgChID, telegram.Message{
		ID: 500, Kind: telegram.KindDocument, MIME: "video/mp4",
		FileName: "clip.mp4", FileSize: 4, Data: []byte("data"),
		Caption: "#tag only",
	})
	if _, err := app.Import(ctx, ImportOptions{Unmanaged: true, NoHash: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Scan(ctx, ScanOptions{Full: true}); err != nil {
		t.Fatal(err)
	}
	if got := fileStatus(t, app, "/videos/clip.mp4"); got != "active" {
		t.Fatalf("status=%q after full scan", got)
	}
}
