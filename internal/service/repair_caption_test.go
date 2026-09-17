package service

import (
	"context"
	"strings"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/core/pathcodec"
)

func oldRenderedCaption(t *testing.T, app *App, channelID int64, path, display, prefix string) string {
	t.Helper()
	tags, _, err := pathcodec.GenerateChain(path, app.loadSlugMap(context.Background(), channelID))
	if err != nil {
		t.Fatal(err)
	}
	parts := []string{display, strings.TrimPrefix(path[:len(path)-len(display)], "/")}
	if parts[1] != "" {
		parts[1] = strings.TrimSuffix(parts[1], "/") + "/"
	}
	parts = append(parts, "", strings.Join(tags, "\n"))
	scaffold := strings.Join(parts, "\n")
	if prefix == "" {
		return scaffold
	}
	return prefix + "\n\n" + scaffold
}

func TestRepairCaptionsRemovesModernScaffold(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "caption body")
	result, err := app.UploadFile(ctx, local, "/stash-browse/832/clip.mp4", ConflictFail, false)
	if err != nil {
		t.Fatal(err)
	}
	channelID, _, err := app.channelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tgChannelID, _ := app.tgChannelID(ctx)
	messageID := result["message_id"].(int)
	if err := tg.EditCaption(ctx, tgChannelID, messageID,
		oldRenderedCaption(t, app, channelID, "/stash-browse/832/clip.mp4", "clip.mp4", "source title")); err != nil {
		t.Fatal(err)
	}

	dry, err := app.RepairCaptions(ctx, "/stash-browse/832", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if dry["planned"] != 1 || dry["cleaned"] != 0 {
		t.Fatalf("dry run = %+v", dry)
	}
	before, err := tg.GetMessage(ctx, tgChannelID, messageID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(before.Caption, "#td_") {
		t.Fatalf("dry run changed caption: %q", before.Caption)
	}

	res, err := app.RepairCaptions(ctx, "/stash-browse/832", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if res["cleaned"] != 1 || res["failed"] != 0 {
		t.Fatalf("cleanup = %+v", res)
	}
	after, err := tg.GetMessage(ctx, tgChannelID, messageID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Caption != "source title" {
		t.Fatalf("cleaned caption = %q", after.Caption)
	}
	foundManifest := false
	for _, message := range machineRecords(t, app, ctx) {
		if strings.Contains(message.Text, "td-manifest:v1") && message.ReplyTo != nil {
			foundManifest = true
			break
		}
	}
	if !foundManifest {
		t.Fatal("caption cleanup removed or failed to preserve discussion manifest")
	}

	idempotent, err := app.RepairCaptions(ctx, "/stash-browse/832", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if idempotent["cleaned"] != 0 || idempotent["skipped"] != 1 {
		t.Fatalf("idempotent cleanup = %+v", idempotent)
	}
}

func TestRepairCaptionsCleansOnlyCaptionedAlbumMember(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	locals := writeLocals(t, 3)
	if _, err := app.UploadFilesAs(ctx, locals, "/albums/", ConflictFail, false, Presentation{}); err != nil {
		t.Fatal(err)
	}
	channelID, _, err := app.channelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tgChannelID, _ := app.tgChannelID(ctx)
	members := groupedMembers(tg.Messages(tgChannelID))
	if len(members) != 3 {
		t.Fatalf("members = %d", len(members))
	}
	if err := tg.EditCaption(ctx, tgChannelID, members[0].ID,
		oldRenderedCaption(t, app, channelID, "/albums/a.bin", "a.bin", "album title")); err != nil {
		t.Fatal(err)
	}

	res, err := app.RepairCaptions(ctx, "/albums", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if res["cleaned"] != 1 || res["failed"] != 0 {
		t.Fatalf("cleanup = %+v", res)
	}
	for _, member := range members {
		got, err := tg.GetMessage(ctx, tgChannelID, member.ID)
		if err != nil {
			t.Fatal(err)
		}
		if member.ID == members[0].ID && got.Caption != "album title" {
			t.Fatalf("first caption = %q", got.Caption)
		}
		if member.ID != members[0].ID && got.Caption != "" {
			t.Fatalf("sibling %d caption = %q", member.ID, got.Caption)
		}
	}
}

func TestRepairCaptionsSkipsUneditableMessage(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	local := writeLocal(t, "caption body")
	result, err := app.UploadFile(ctx, local, "/old/clip.mp4", ConflictFail, false)
	if err != nil {
		t.Fatal(err)
	}
	channelID, _, err := app.channelID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tgChannelID, _ := app.tgChannelID(ctx)
	messageID := result["message_id"].(int)
	if err := tg.EditCaption(ctx, tgChannelID, messageID,
		oldRenderedCaption(t, app, channelID, "/old/clip.mp4", "clip.mp4", "")); err != nil {
		t.Fatal(err)
	}
	tg.SetNotEditable(tgChannelID, messageID, true)

	res, err := app.RepairCaptions(ctx, "/old", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if res["skipped"] != 1 || res["cleaned"] != 0 || res["failed"] != 0 {
		t.Fatalf("cleanup = %+v", res)
	}
}
