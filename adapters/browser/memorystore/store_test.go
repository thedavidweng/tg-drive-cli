package memorystore

import (
	"context"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/core/model"
	"github.com/thedavidweng/tg-drive-cli/core/pathcodec"
)

func TestMemoryStoreSlugRoundTrip(t *testing.T) {
	store := New()
	channel := model.ChannelID(1)
	store.PutSlug(channel, "/|docs", "docs_ab12c")
	slugs, err := store.LoadSlugMap(context.Background(), channel)
	if err != nil {
		t.Fatal(err)
	}
	tags, _, err := pathcodec.GenerateChain("/docs/readme.txt", slugs)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0] == "" {
		t.Fatalf("tags = %v", tags)
	}
}
