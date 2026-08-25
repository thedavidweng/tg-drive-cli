package service

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lukechampine.com/blake3"

	"github.com/thedavidweng/tg-drive-cli/core/manifest"
)

// blake3Of hashes content the way uploads and backfill store it.
func blake3Of(b []byte) string {
	h := blake3.New(32, nil)
	_, _ = h.Write(b)
	return "blake3:" + hex.EncodeToString(h.Sum(nil))
}

// stripHashes clears the stored content hash of every active file, simulating
// rows adopted without --hash.
func stripHashes(t *testing.T, app *App) {
	t.Helper()
	if _, err := app.DB.Raw().Exec(`update files set content_hash='' where status='active'`); err != nil {
		t.Fatal(err)
	}
}

// TestRepairHashBackfillsSingleAndAlbum pins the hash backfill contract:
// files without a content hash are downloaded once, the BLAKE3 digest lands
// in the index AND in the machine record (per-file comment, album inventory
// comment), rows that already carry a hash are skipped, and a zero size is
// repaired from the download.
func TestRepairHashBackfillsSingleAndAlbum(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()

	// One single file and one three-member album.
	single := filepath.Join(t.TempDir(), "solo.bin")
	singleContent := []byte("solo hash me")
	if err := os.WriteFile(single, singleContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, single, "/backfill/solo.bin", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	locals := writeLocals(t, 3)
	if _, err := app.UploadFilesAs(ctx, locals, "/backfill/albums/", ConflictFail, false, Presentation{}); err != nil {
		t.Fatal(err)
	}
	stripHashes(t, app)

	res, err := app.RepairHash(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if res["backfilled"].(int) != 4 || res["failed"].(int) != 0 {
		t.Fatalf("res = backfilled %v failed %v", res["backfilled"], res["failed"])
	}

	// Index: hashes present and byte-exact.
	var soloHash string
	if err := app.DB.Raw().QueryRow(`select content_hash from files where canonical_path='/backfill/solo.bin' and status='active'`).Scan(&soloHash); err != nil {
		t.Fatal(err)
	}
	if soloHash != blake3Of(singleContent) {
		t.Fatalf("solo hash = %q", soloHash)
	}
	for _, p := range []string{"/backfill/albums/a.bin", "/backfill/albums/b.bin", "/backfill/albums/c.bin"} {
		var h string
		if err := app.DB.Raw().QueryRow(`select content_hash from files where canonical_path=? and status='active'`, p).Scan(&h); err != nil {
			t.Fatal(err)
		}
		if h == "" {
			t.Fatalf("%s still without hash", p)
		}
	}

	// Machine record: the solo file's comment carries the new hash.
	var found int
	for _, m := range machineRecords(t, app, ctx) {
		if strings.HasPrefix(m.Text, "td-manifest:v1") && strings.Contains(m.Text, "p="+b64url("/backfill/solo.bin")) {
			if strings.Contains(m.Text, soloHash) {
				found++
			}
		}
	}
	if found != 1 {
		t.Fatalf("solo comment records with new hash = %d, want 1", found)
	}

	// Album inventory comment carries every member hash.
	inventories := albumInventoryComments(t, app, ctx)
	if len(inventories) != 1 {
		t.Fatalf("album inventories = %d, want 1", len(inventories))
	}
	for _, msg := range inventories {
		meta, err := manifest.ParseAlbumReply(msg.Text)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range meta.Files {
			if f.Hash == "" {
				t.Fatalf("inventory member %s without hash: %+v", f.CanonicalPath, meta.Files)
			}
		}
	}

	// A second run is a no-op: rows already carry hashes.
	res2, err := app.RepairHash(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if res2["backfilled"].(int) != 0 {
		t.Fatalf("second backfill = %v, want 0", res2["backfilled"])
	}
}

// TestRepairHashPathScopeAndSkip pins the scoping contract: only the subtree
// is processed, and rows with a hash are never touched.
func TestRepairHashPathScopeAndSkip(t *testing.T) {
	app, tg := testApp(t)
	loginAndInit(t, app, tg)
	ctx := context.Background()
	in := writeLocal(t, "in scope")
	out := writeLocal(t, "out of scope")
	if _, err := app.UploadFile(ctx, in, "/scope/in.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UploadFile(ctx, out, "/other/out.txt", ConflictFail, false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DB.Raw().Exec(`update files set content_hash='' where canonical_path in ('/scope/in.txt','/other/out.txt')`); err != nil {
		t.Fatal(err)
	}
	res, err := app.RepairHash(ctx, "/scope")
	if err != nil {
		t.Fatal(err)
	}
	if res["backfilled"].(int) != 1 || res["total"].(int) != 1 {
		t.Fatalf("res = %v, want one scoped backfill", res)
	}
	var outHash string
	if err := app.DB.Raw().QueryRow(`select content_hash from files where canonical_path='/other/out.txt'`).Scan(&outHash); err != nil {
		t.Fatal(err)
	}
	if outHash != "" {
		t.Fatal("out-of-scope row was modified")
	}
}
