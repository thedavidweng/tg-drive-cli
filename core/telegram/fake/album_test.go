package fake

import (
	"context"
	"strings"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// groupReq builds one group-member upload request.
func groupReq(channelID int64, name string, kind string) telegram.UploadRequest {
	return telegram.UploadRequest{
		ChannelID: channelID,
		Caption:   "caption of " + name,
		FileName:  name,
		MIME:      "application/octet-stream",
		Size:      int64(len(name)),
		Reader:    strings.NewReader(name),
		Kind:      kind,
	}
}

func loginGroup(t *testing.T, c *Client) int64 {
	t.Helper()
	if _, err := c.Login(context.Background(), 1, "h", "+1",
		func(telegram.CodePrompt) (string, error) { return "12345", nil },
		nil, telegram.LoginOptions{}); err != nil {
		t.Fatal(err)
	}
	ch, err := c.CreateChannel(context.Background(), "Albums")
	if err != nil {
		t.Fatal(err)
	}
	return ch.ID
}

// TestUploadMediaGroupShape pins the observable sendMultiMedia behavior the
// service and scan rely on: shared grouped id, first-caption-only, ordered
// results.
func TestUploadMediaGroupShape(t *testing.T) {
	c := New()
	chID := loginGroup(t, c)

	res, err := c.UploadMediaGroup(context.Background(), []telegram.UploadRequest{
		groupReq(chID, "one.bin", telegram.KindDocument),
		groupReq(chID, "two.bin", telegram.KindDocument),
		groupReq(chID, "three.bin", telegram.KindDocument),
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs := c.Messages(chID)
	if len(msgs) != 3 || len(res) != 3 {
		t.Fatalf("messages=%d results=%d, want 3/3", len(msgs), len(res))
	}
	gid := res[0].GroupedID
	if gid == 0 {
		t.Fatal("grouped id must not be zero")
	}
	for i := range res {
		if res[i].MessageID != msgs[i].ID || res[i].GroupedID != gid {
			t.Fatalf("res[%d] = %+v, msg %+v", i, res[i], msgs[i])
		}
	}
	if msgs[0].Caption != "caption of one.bin" {
		t.Fatalf("first caption = %q", msgs[0].Caption)
	}
	for _, m := range msgs[1:] {
		if m.Caption != "" || m.GroupedID != gid {
			t.Fatalf("sibling caption/group = %q/%d", m.Caption, m.GroupedID)
		}
	}

	// A second group gets its own grouped id.
	res2, err := c.UploadMediaGroup(context.Background(), []telegram.UploadRequest{
		groupReq(chID, "four.bin", telegram.KindDocument),
		groupReq(chID, "five.bin", telegram.KindDocument),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2[0].GroupedID == gid {
		t.Fatal("second group reused the grouped id")
	}
}

// TestUploadMediaGroupRejectsInvalid pins the group guards: size limits and
// channel/kind uniformity.
func TestUploadMediaGroupRejectsInvalid(t *testing.T) {
	c := New()
	chID := loginGroup(t, c)
	ctx := context.Background()

	if _, err := c.UploadMediaGroup(ctx, nil); err == nil {
		t.Fatal("empty group must error")
	}
	var many []telegram.UploadRequest
	for i := 0; i <= telegram.MaxMediaGroupMembers; i++ {
		many = append(many, groupReq(chID, "f.bin", telegram.KindDocument))
	}
	if _, err := c.UploadMediaGroup(ctx, many); err == nil {
		t.Fatal(">10 members must error")
	}

	mixed := []telegram.UploadRequest{
		groupReq(chID, "a.bin", telegram.KindDocument),
		groupReq(chID, "b.jpg", telegram.KindPhoto),
	}
	if _, err := c.UploadMediaGroup(ctx, mixed); err == nil {
		t.Fatal("mixed kinds must error")
	}
	cross := []telegram.UploadRequest{
		groupReq(chID, "a.bin", telegram.KindDocument),
		groupReq(chID+1, "b.bin", telegram.KindDocument),
	}
	if _, err := c.UploadMediaGroup(ctx, cross); err == nil {
		t.Fatal("mixed channels must error")
	}

	// A failed member leaves no partial group behind.
	failing := groupReq(chID, "ok.bin", telegram.KindDocument)
	failing.Reader = errReader{}
	okReq := groupReq(chID, "fine.bin", telegram.KindDocument)
	if _, err := c.UploadMediaGroup(ctx, []telegram.UploadRequest{okReq, failing}); err == nil {
		t.Fatal("member failure must surface")
	}
	if got := len(c.Messages(chID)); got != 0 {
		t.Fatalf("partial group left %d messages", got)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, context.DeadlineExceeded }
