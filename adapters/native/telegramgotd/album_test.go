package telegramgotd

import (
	"testing"

	"github.com/gotd/td/tg"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// TestBuildAlbumInputMedia pins the sendMultiMedia input mapping: photo kind
// → uploaded photo, video kind → document with filename + video attributes,
// document kind → document with filename, thumbs attached to documents only.
func TestBuildAlbumInputMedia(t *testing.T) {
	file := &tg.InputFile{ID: 7, Parts: 1}
	thumb := &tg.InputFile{ID: 8, Parts: 1}

	photo := buildAlbumInputMedia(file, nil, tgtelegram.UploadRequest{Kind: tgtelegram.KindPhoto})
	if _, ok := photo.(*tg.InputMediaUploadedPhoto); !ok {
		t.Fatalf("photo kind produced %T", photo)
	}

	doc := buildAlbumInputMedia(file, thumb, tgtelegram.UploadRequest{
		Kind: tgtelegram.KindVideo, FileName: "scene.mp4", MIME: "video/mp4",
		Video: &tgtelegram.VideoAttributes{DurationSeconds: 12, Width: 640, Height: 360, SupportsStreaming: true},
	})
	uploadedDoc, ok := doc.(*tg.InputMediaUploadedDocument)
	if !ok {
		t.Fatalf("video kind produced %T", doc)
	}
	if uploadedDoc.MimeType != "video/mp4" {
		t.Fatalf("mime = %q", uploadedDoc.MimeType)
	}
	if uploadedDoc.Thumb == nil {
		t.Fatal("thumb not attached")
	}
	var filename *tg.DocumentAttributeFilename
	var videoAttr *tg.DocumentAttributeVideo
	for _, attr := range uploadedDoc.Attributes {
		switch a := attr.(type) {
		case *tg.DocumentAttributeFilename:
			filename = a
		case *tg.DocumentAttributeVideo:
			videoAttr = a
		}
	}
	if filename == nil || filename.FileName != "scene.mp4" {
		t.Fatalf("filename attribute = %+v", filename)
	}
	if videoAttr == nil || videoAttr.Duration != 12 || videoAttr.W != 640 || videoAttr.H != 360 || !videoAttr.SupportsStreaming {
		t.Fatalf("video attribute = %+v", videoAttr)
	}

	plain := buildAlbumInputMedia(file, thumb, tgtelegram.UploadRequest{
		Kind: tgtelegram.KindDocument, FileName: "note.txt", MIME: "text/plain",
	})
	plainDoc := plain.(*tg.InputMediaUploadedDocument)
	if plainDoc.Thumb != thumb || len(plainDoc.Attributes) != 1 {
		t.Fatalf("document must keep the thumb and only the filename attribute, got %+v / thumb %v", plainDoc.Attributes, plainDoc.Thumb)
	}

	// Photos ignore thumbs outright (service validation rejects them anyway).
	noThumbPhoto := buildAlbumInputMedia(file, thumb, tgtelegram.UploadRequest{Kind: tgtelegram.KindPhoto})
	photoMedia := noThumbPhoto.(*tg.InputMediaUploadedPhoto)
	if photoMedia.File != file {
		t.Fatalf("photo media file = %+v", photoMedia.File)
	}
}

func mediaGroupUpdates(ids ...int) *tg.Updates {
	upd := &tg.Updates{}
	for _, id := range ids {
		msg := &tg.Message{ID: id}
		msg.SetGroupedID(4242)
		upd.Updates = append(upd.Updates, &tg.UpdateNewChannelMessage{Message: msg})
	}
	return upd
}

// TestExtractMediaGroupResults covers id ordering, grouped-id extraction, and
// the mismatch guards.
func TestExtractMediaGroupResults(t *testing.T) {
	// Out-of-order updates normalize to ascending message ids.
	res, err := extractMediaGroupResults(mediaGroupUpdates(31, 30, 32), 3)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{30, 31, 32} {
		if res[i].MessageID != want || res[i].GroupedID != 4242 {
			t.Fatalf("res[%d] = %+v", i, res[i])
		}
	}

	if _, err := extractMediaGroupResults(mediaGroupUpdates(30, 31), 3); err == nil {
		t.Fatal("missing member must error")
	}

	bad := mediaGroupUpdates(30, 31)
	bad.Updates = append(bad.Updates, &tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 32}})
	if _, err := extractMediaGroupResults(bad, 3); err == nil {
		t.Fatal("member without grouped id must error")
	}
}

// TestMediaReference pins the two-phase album contract: the MessageMedia
// returned by messages.uploadMedia becomes a referencing inputMedia
// constructor carrying id, access hash, and file reference.
func TestMediaReference(t *testing.T) {
	photo := &tg.Photo{ID: 11, AccessHash: 22, FileReference: []byte("ref")}
	ref, err := mediaReference(&tg.MessageMediaPhoto{Photo: photo})
	if err != nil {
		t.Fatal(err)
	}
	pm, ok := ref.(*tg.InputMediaPhoto)
	if !ok {
		t.Fatalf("photo media reference = %T", ref)
	}
	inPhoto, ok := pm.ID.(*tg.InputPhoto)
	if !ok || inPhoto.ID != 11 || inPhoto.AccessHash != 22 || string(inPhoto.FileReference) != "ref" {
		t.Fatalf("photo input = %+v", pm.ID)
	}

	doc := &tg.Document{ID: 33, AccessHash: 44, FileReference: []byte("dref")}
	dref, err := mediaReference(&tg.MessageMediaDocument{Document: doc})
	if err != nil {
		t.Fatal(err)
	}
	dm, ok := dref.(*tg.InputMediaDocument)
	if !ok {
		t.Fatalf("document media reference = %T", dref)
	}
	inDoc, ok := dm.ID.(*tg.InputDocument)
	if !ok || inDoc.ID != 33 || inDoc.AccessHash != 44 || string(inDoc.FileReference) != "dref" {
		t.Fatalf("document input = %+v", dm.ID)
	}

	for _, bad := range []tg.MessageMediaClass{
		&tg.MessageMediaEmpty{},
		&tg.MessageMediaPhoto{Photo: &tg.PhotoEmpty{}},
	} {
		if _, err := mediaReference(bad); err == nil {
			t.Fatalf("%T must error", bad)
		}
	}
}
