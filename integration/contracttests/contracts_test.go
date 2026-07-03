package contracttests

import (
	"context"
	"testing"

	"github.com/thedavidweng/tg-drive-cli/adapters/browser/memorystore"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/model"
	"github.com/thedavidweng/tg-drive-cli/core/pathcodec"
)

func TestPathContractFixturesNormalize(t *testing.T) {
	cases := []string{
		"/simple/file.txt",
		"/docs/2024/报告.pdf",
		"/a/b/c/d/e/f/g.txt",
	}
	for _, p := range cases {
		got, err := fsmodel.NormalizeCanonicalPath(p)
		if err != nil {
			t.Fatalf("%q: %v", p, err)
		}
		if got != p {
			t.Fatalf("%q => %q", p, got)
		}
	}
}

func TestSlugStoreContractAcrossAdapters(t *testing.T) {
	store := memorystore.New()
	channel := model.ChannelID(42)
	store.PutSlug(channel, "/|中文", pathcodec.SegmentSlug("中文", 5))
	slugs, err := store.LoadSlugMap(context.Background(), channel)
	if err != nil {
		t.Fatal(err)
	}
	tags, _, err := pathcodec.GenerateChain("/中文/file.txt", slugs)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 {
		t.Fatalf("tags = %v", tags)
	}
}
