package store

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"
)

func snapshotStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "polka.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, context.Background()
}

func snapshotLibrary(t *testing.T, st *Store, ctx context.Context, name string) string {
	t.Helper()
	l, err := st.AddLibrary(ctx, Library{Name: name, Kind: "folder", Root: filepath.Join(t.TempDir(), name)})
	if err != nil {
		t.Fatal(err)
	}
	return l.ID
}

func newTestSnapshot(t *testing.T, ctx context.Context, entries ...struct {
	key  string
	book *BookInput
}) *Snapshot {
	t.Helper()
	snap, err := NewSnapshot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = snap.Close() })
	for _, entry := range entries {
		if err := snap.Add(ctx, entry.key, entry.book); err != nil {
			t.Fatal(err)
		}
	}
	return snap
}

func snapshotEntry(key, title string) struct {
	key  string
	book *BookInput
} {
	return struct {
		key  string
		book *BookInput
	}{key, &BookInput{Title: title, Folder: "books", File: key, Ext: "fb2"}}
}

func snapshotBookID(t *testing.T, st *Store, libraryID, key string) int64 {
	t.Helper()
	var id int64
	if err := st.DB().QueryRow(`SELECT id FROM books WHERE library_id = ? AND source_key = ?`, libraryID, key).Scan(&id); err != nil {
		t.Fatalf("book id for %q/%q: %v", libraryID, key, err)
	}
	return id
}

func snapshotState(t *testing.T, st *Store, libraryID, key string) (id int64, missing, deleted int) {
	t.Helper()
	if err := st.DB().QueryRow(`SELECT id, missing, deleted FROM books WHERE library_id = ? AND source_key = ?`, libraryID, key).Scan(&id, &missing, &deleted); err != nil {
		t.Fatalf("book state for %q/%q: %v", libraryID, key, err)
	}
	return
}

func snapshotSearchIndexed(t *testing.T, st *Store, bookID int64) bool {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT count(*) FROM book_search WHERE rowid = ?`, bookID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n != 0
}

func publishTestSnapshot(t *testing.T, st *Store, ctx context.Context, libraryID string, entries ...struct {
	key  string
	book *BookInput
}) error {
	t.Helper()
	snap := newTestSnapshot(t, ctx, entries...)
	_, err := st.PublishLibrary(ctx, libraryID, snap, false)
	return err
}

func TestPublishLibraryKeepsSourceIdentityScopedAndStable(t *testing.T) {
	st, ctx := snapshotStore(t)
	first := snapshotLibrary(t, st, ctx, "first")
	second := snapshotLibrary(t, st, ctx, "second")

	if err := publishTestSnapshot(t, st, ctx, first,
		snapshotEntry("record:1", "First one"), snapshotEntry("record:2", "First two")); err != nil {
		t.Fatal(err)
	}
	firstID := snapshotBookID(t, st, first, "record:1")
	secondID := snapshotBookID(t, st, first, "record:2")
	if firstID == secondID {
		t.Fatalf("distinct source records share ID %d", firstID)
	}
	if err := publishTestSnapshot(t, st, ctx, second, snapshotEntry("record:1", "Second one")); err != nil {
		t.Fatal(err)
	}
	otherID := snapshotBookID(t, st, second, "record:1")
	if otherID == firstID {
		t.Fatalf("same key in separate libraries reused ID %d", firstID)
	}

	if err := publishTestSnapshot(t, st, ctx, first,
		snapshotEntry("record:1", "First one"), snapshotEntry("record:2", "First two")); err != nil {
		t.Fatal(err)
	}
	if got := snapshotBookID(t, st, first, "record:1"); got != firstID {
		t.Fatalf("unchanged key ID = %d, want %d", got, firstID)
	}
	unchangedSnapshot := newTestSnapshot(t, ctx,
		snapshotEntry("record:1", "First one"), snapshotEntry("record:2", "First two"))
	stats, err := st.PublishLibrary(ctx, first, unchangedSnapshot, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Changed != 0 || stats.Unchanged != 2 || stats.Added != 0 || stats.Removed != 0 {
		t.Fatalf("unchanged refresh stats = %+v", stats)
	}
	if got := snapshotBookID(t, st, second, "record:1"); got != otherID {
		t.Fatalf("publishing first library changed second library ID to %d, want %d", got, otherID)
	}

	if err := st.SetBookDeleted(ctx, secondID, true); err != nil {
		t.Fatal(err)
	}
	removedSnapshot := newTestSnapshot(t, ctx, snapshotEntry("record:1", "First one"))
	stats, err = st.PublishLibrary(ctx, first, removedSnapshot, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Removed != 1 || stats.Unchanged != 1 {
		t.Fatalf("removal stats = %+v", stats)
	}
	gotID, missing, deleted := snapshotState(t, st, first, "record:2")
	if gotID != secondID || missing != 1 || deleted != 1 {
		t.Fatalf("removed source state = id %d missing %d deleted %d", gotID, missing, deleted)
	}
	if snapshotSearchIndexed(t, st, secondID) {
		t.Fatal("removed source remained in the full-text search index")
	}
	if !snapshotSearchIndexed(t, st, otherID) {
		t.Fatal("publishing first library removed the other library's search row")
	}
	if _, missing, _ := snapshotState(t, st, second, "record:1"); missing != 0 {
		t.Fatalf("updating first library marked second library's row missing")
	}

	reappeared := newTestSnapshot(t, ctx, snapshotEntry("record:1", "First one"), snapshotEntry("record:2", "First two"))
	stats, err = st.PublishLibrary(ctx, first, reappeared, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Changed != 1 || stats.Unchanged != 1 || stats.Removed != 0 {
		t.Fatalf("reappearance stats = %+v", stats)
	}
	gotID, missing, deleted = snapshotState(t, st, first, "record:2")
	if gotID != secondID || missing != 0 || deleted != 1 {
		t.Fatalf("reappeared source state = id %d missing %d deleted %d", gotID, missing, deleted)
	}
	if snapshotSearchIndexed(t, st, secondID) {
		t.Fatal("reappeared source overrode its admin-deleted state")
	}
	if err := st.SetBookDeleted(ctx, secondID, false); err != nil {
		t.Fatal(err)
	}
	if !snapshotSearchIndexed(t, st, secondID) {
		t.Fatal("admin restore did not put the present source back in search")
	}

	if err := publishTestSnapshot(t, st, ctx, first,
		snapshotEntry("record:1:changed", "Changed bytes"), snapshotEntry("record:2", "First two")); err != nil {
		t.Fatal(err)
	}
	changedID := snapshotBookID(t, st, first, "record:1:changed")
	if changedID <= otherID || changedID <= secondID {
		t.Fatalf("changed key ID = %d, want greater than existing IDs %d and %d", changedID, otherID, secondID)
	}
	if _, missing, _ := snapshotState(t, st, first, "record:1"); missing != 1 {
		t.Fatal("old source key was not retained as missing")
	}
}

func TestPublishLibraryCountsUnknownLegacyRowsRemovedButPreservesUploads(t *testing.T) {
	st, ctx := snapshotStore(t)
	unknownID, err := st.AddBook(ctx, &BookInput{Title: "Unknown legacy", Folder: "old", File: "unknown.fb2", Ext: "fb2"})
	if err != nil {
		t.Fatal(err)
	}
	uploadID, err := st.AddBook(ctx, &BookInput{Title: "Uploaded book", Folder: "uploads", File: "upload.fb2", Ext: "fb2", FileHash: "upload-hash"})
	if err != nil {
		t.Fatal(err)
	}
	snap := newTestSnapshot(t, ctx, snapshotEntry("inpx:record", "Catalog book"))
	snap.SetInpxSource("catalog.inpx", "fingerprint", "collection", "/catalog.inpx", true)
	stats, err := st.PublishLibrary(ctx, "legacy", snap, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Removed != 1 {
		t.Fatalf("removed count = %d, want 1 unknown legacy row", stats.Removed)
	}
	var missing int
	if err := st.DB().QueryRow(`SELECT missing FROM books WHERE id=?`, unknownID).Scan(&missing); err != nil || missing != 1 {
		t.Fatalf("unknown legacy row missing = %d, %v; want 1", missing, err)
	}
	if err := st.DB().QueryRow(`SELECT missing FROM books WHERE id=?`, uploadID).Scan(&missing); err != nil || missing != 0 {
		t.Fatalf("uploaded row missing = %d, %v; want 0", missing, err)
	}
}

func TestPublishLibraryRejectsInvalidSnapshotsWithoutChangingPublishedState(t *testing.T) {
	st, ctx := snapshotStore(t)
	libraryID := snapshotLibrary(t, st, ctx, "atomic")
	if err := publishTestSnapshot(t, st, ctx, libraryID, snapshotEntry("old", "Published")); err != nil {
		t.Fatal(err)
	}
	oldID := snapshotBookID(t, st, libraryID, "old")
	oldHighWater := st.GetMeta(ctx, bookIDHighWaterKey, "")
	var oldSearch int
	if err := st.DB().QueryRow(`SELECT count(*) FROM book_search`).Scan(&oldSearch); err != nil {
		t.Fatal(err)
	}

	duplicate := newTestSnapshot(t, ctx, snapshotEntry("dup", "One"))
	if err := duplicate.Add(ctx, "dup", &BookInput{Title: "Two", Folder: "books", File: "dup"}); err == nil {
		t.Fatal("duplicate source key was accepted")
	}
	if _, err := st.PublishLibrary(ctx, libraryID, duplicate, false); err == nil {
		t.Fatal("poisoned snapshot was published")
	}

	if _, err := st.PublishLibrary(ctx, libraryID, newTestSnapshot(t, ctx), false); err == nil {
		t.Fatal("empty snapshot was published without explicit permission")
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	cancelledSnapshot := newTestSnapshot(t, ctx, snapshotEntry("new", "Cancelled"))
	if _, err := st.PublishLibrary(cancelled, libraryID, cancelledSnapshot, false); err == nil {
		t.Fatal("publication with a cancelled context succeeded")
	}

	if _, err := st.DB().Exec(`CREATE TRIGGER reject_snapshot_row BEFORE INSERT ON books WHEN NEW.title = 'reject' BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	failing := newTestSnapshot(t, ctx,
		snapshotEntry("new-first", "Inserted before failure"), snapshotEntry("new-reject", "reject"))
	if _, err := st.PublishLibrary(ctx, libraryID, failing, false); err == nil {
		t.Fatal("publication with an insert failure succeeded")
	}

	if got := snapshotBookID(t, st, libraryID, "old"); got != oldID {
		t.Fatalf("failed publication changed old ID to %d, want %d", got, oldID)
	}
	if _, err := snapshotBookIDNoFail(st, libraryID, "new-first"); err == nil {
		t.Fatal("failed publication left a partially inserted row")
	}
	if _, missing, _ := snapshotState(t, st, libraryID, "old"); missing != 0 {
		t.Fatal("failed publication marked the prior row missing")
	}
	if got := st.GetMeta(ctx, bookIDHighWaterKey, ""); got != oldHighWater {
		t.Fatalf("failed publication changed allocation floor to %q, want %q", got, oldHighWater)
	}
	var searchCount int
	if err := st.DB().QueryRow(`SELECT count(*) FROM book_search`).Scan(&searchCount); err != nil || searchCount != oldSearch {
		t.Fatalf("failed publication changed search rows: count=%d want=%d err=%v", searchCount, oldSearch, err)
	}
}

func TestPublishLibraryLeavesLegacyUploadRowsWithoutSourceKeysAlone(t *testing.T) {
	st, ctx := snapshotStore(t)
	if _, err := st.AddBook(ctx, &BookInput{Title: "Uploaded", Folder: "upload", File: "upload.fb2", Ext: "fb2"}); err != nil {
		t.Fatal(err)
	}
	var uploadID int64
	if err := st.DB().QueryRow(`SELECT id FROM books WHERE title='Uploaded'`).Scan(&uploadID); err != nil {
		t.Fatal(err)
	}
	if err := publishTestSnapshot(t, st, ctx, "legacy", snapshotEntry("inpx:42", "Imported")); err != nil {
		t.Fatal(err)
	}
	var libraryID, sourceKey string
	var missing int
	if err := st.DB().QueryRow(`SELECT library_id,source_key,missing FROM books WHERE id=?`, uploadID).Scan(&libraryID, &sourceKey, &missing); err != nil {
		t.Fatal(err)
	}
	if libraryID != "legacy" || sourceKey != "" || missing != 0 {
		t.Fatalf("legacy upload state = library %q key %q missing %d", libraryID, sourceKey, missing)
	}
}

func TestPublishLibraryMapsSourceDeletedToMissing(t *testing.T) {
	st, ctx := snapshotStore(t)
	libraryID := snapshotLibrary(t, st, ctx, "deleted-source")
	deleted := snapshotEntry("record:gone", "Source deleted")
	deleted.book.Deleted = true
	if err := publishTestSnapshot(t, st, ctx, libraryID, deleted); err != nil {
		t.Fatal(err)
	}
	id, missing, adminDeleted := snapshotState(t, st, libraryID, "record:gone")
	if missing != 1 || adminDeleted != 0 || snapshotSearchIndexed(t, st, id) {
		t.Fatalf("source-deleted row = missing %d admin-deleted %d indexed %v", missing, adminDeleted, snapshotSearchIndexed(t, st, id))
	}
	if err := publishTestSnapshot(t, st, ctx, libraryID, snapshotEntry("record:gone", "Source deleted")); err != nil {
		t.Fatal(err)
	}
	gotID, missing, adminDeleted := snapshotState(t, st, libraryID, "record:gone")
	if gotID != id || missing != 0 || adminDeleted != 0 || !snapshotSearchIndexed(t, st, id) {
		t.Fatalf("source restore = id %d missing %d admin-deleted %d indexed %v", gotID, missing, adminDeleted, snapshotSearchIndexed(t, st, id))
	}
}

func snapshotBookIDNoFail(st *Store, libraryID, key string) (int64, error) {
	var id int64
	err := st.DB().QueryRow(`SELECT id FROM books WHERE library_id = ? AND source_key = ?`, libraryID, key).Scan(&id)
	return id, err
}

// A staged inpx:v2 group rebinds the per-series v1 rows of its file: the
// lowest-ID row wins and keeps its identity with the merged content, the rest
// retire to missing and leave the search index.
func TestPublishLibraryConsolidatesInpxV1RowsIntoV2Group(t *testing.T) {
	st, ctx := snapshotStore(t)
	libraryID := snapshotLibrary(t, st, ctx, "consolidated")

	alpha := snapshotEntry("inpx:v1:alpha", "Series book")
	alpha.book.File = "multi"
	alpha.book.Series = "Alpha"
	alpha.book.SeriesNum = 1
	beta := snapshotEntry("inpx:v1:beta", "Series book")
	beta.book.File = "multi"
	beta.book.Series = "Beta"
	beta.book.SeriesNum = 2
	single := snapshotEntry("inpx:v1:single", "Old title")
	single.book.File = "single"
	single.book.Series = "OldSeries"
	if err := publishTestSnapshot(t, st, ctx, libraryID, alpha, beta, single); err != nil {
		t.Fatal(err)
	}
	winnerID := snapshotBookID(t, st, libraryID, "inpx:v1:alpha")
	loserID := snapshotBookID(t, st, libraryID, "inpx:v1:beta")
	singleID := snapshotBookID(t, st, libraryID, "inpx:v1:single")
	if winnerID >= loserID {
		t.Fatalf("fixture IDs out of order: winner %d, loser %d", winnerID, loserID)
	}
	if !snapshotSearchIndexed(t, st, loserID) {
		t.Fatal("fixture loser was never indexed")
	}

	group := snapshotEntry("inpx:v2:group", "Series book (union)")
	group.book.File = "multi"
	group.book.Series = "Alpha"
	group.book.SeriesNum = 1
	group.book.SeriesList = []SeriesRef{{Title: "Alpha", SeqNumber: 1}, {Title: "Beta", SeqNumber: 2}}
	group.book.Year = 2020
	renamed := snapshotEntry("inpx:v2:single", "New title")
	renamed.book.File = "single"
	renamed.book.Series = "NewSeries"
	renamed.book.SeriesNum = 3
	snap := newTestSnapshot(t, ctx, group, renamed)
	stats, err := st.PublishLibrary(ctx, libraryID, snap, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 0 || stats.Changed != 2 || stats.Removed != 1 || stats.Unchanged != 0 {
		t.Fatalf("consolidation stats = %+v, want 2 rekeys, 1 swept loser", stats)
	}
	if len(stats.ConsolidatedIDs) != 1 || stats.ConsolidatedIDs[loserID] != winnerID {
		t.Fatalf("consolidation pairs = %v, want %d -> %d", stats.ConsolidatedIDs, loserID, winnerID)
	}

	// The winner keeps its ID, takes the v2 key and the merged metadata.
	if got := snapshotBookID(t, st, libraryID, "inpx:v2:group"); got != winnerID {
		t.Fatalf("rekeyed group ID = %d, want kept winner ID %d", got, winnerID)
	}
	gotID, missing, deleted := snapshotState(t, st, libraryID, "inpx:v2:group")
	if gotID != winnerID || missing != 0 || deleted != 0 {
		t.Fatalf("rekeyed winner state = id %d missing %d deleted %d", gotID, missing, deleted)
	}
	var title string
	var year int
	if err := st.DB().QueryRow(`SELECT title, year FROM books WHERE id=?`, winnerID).Scan(&title, &year); err != nil {
		t.Fatal(err)
	}
	if title != "Series book (union)" || year != 2020 {
		t.Fatalf("rekeyed winner metadata = %q (%d), want merged group content", title, year)
	}
	refs, err := st.BookSeries(ctx, winnerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].Title != "Alpha" || refs[1].Title != "Beta" {
		t.Fatalf("winner series = %+v, want merged Alpha and Beta", refs)
	}
	if !snapshotSearchIndexed(t, st, winnerID) {
		t.Fatal("rekeyed winner is missing from the search index")
	}

	// Losers keep their absent v1 keys: swept to missing, out of the index.
	gotID, missing, deleted = snapshotState(t, st, libraryID, "inpx:v1:beta")
	if gotID != loserID || missing != 1 || deleted != 0 {
		t.Fatalf("loser state = id %d missing %d deleted %d, want retained missing", gotID, missing, deleted)
	}
	if snapshotSearchIndexed(t, st, loserID) {
		t.Fatal("loser remained in the search index")
	}

	// A single matching row rekeys in place too, updating metadata and series.
	if got := snapshotBookID(t, st, libraryID, "inpx:v2:single"); got != singleID {
		t.Fatalf("single-match rekey ID = %d, want kept ID %d", got, singleID)
	}
	refs, err = st.BookSeries(ctx, singleID)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Title != "NewSeries" || refs[0].SeqNumber != 3 {
		t.Fatalf("single-match series = %+v, want NewSeries #3", refs)
	}
	if _, err := snapshotBookIDNoFail(st, libraryID, "inpx:v1:single"); err == nil {
		t.Fatal("single-match row kept its old v1 key after rekey")
	}
}

// A consolidation winner that was already missing is restored by the rekey.
func TestPublishLibraryConsolidationRestoresMissingV1Winner(t *testing.T) {
	st, ctx := snapshotStore(t)
	libraryID := snapshotLibrary(t, st, ctx, "restore")
	gone := snapshotEntry("inpx:v1:gone", "Gone book")
	gone.book.File = "gone"
	if err := publishTestSnapshot(t, st, ctx, libraryID, gone); err != nil {
		t.Fatal(err)
	}
	goneID := snapshotBookID(t, st, libraryID, "inpx:v1:gone")
	if err := publishTestSnapshot(t, st, ctx, libraryID, snapshotEntry("record:unrelated", "Unrelated")); err != nil {
		t.Fatal(err)
	}
	if _, missing, _ := snapshotState(t, st, libraryID, "inpx:v1:gone"); missing != 1 {
		t.Fatal("fixture row was not retired to missing")
	}
	if snapshotSearchIndexed(t, st, goneID) {
		t.Fatal("missing fixture row stayed in the search index")
	}

	group := snapshotEntry("inpx:v2:gone", "Gone book")
	group.book.File = "gone"
	snap := newTestSnapshot(t, ctx, group, snapshotEntry("record:unrelated", "Unrelated"))
	stats, err := st.PublishLibrary(ctx, libraryID, snap, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Changed != 1 || stats.Unchanged != 1 {
		t.Fatalf("restore consolidation stats = %+v, want 1 changed 1 unchanged", stats)
	}
	gotID, missing, _ := snapshotState(t, st, libraryID, "inpx:v2:gone")
	if gotID != goneID || missing != 0 {
		t.Fatalf("restored winner = id %d missing %d, want id %d missing 0", gotID, missing, goneID)
	}
	if !snapshotSearchIndexed(t, st, goneID) {
		t.Fatal("restored winner is missing from the search index")
	}
	if len(stats.ConsolidatedIDs) != 0 {
		t.Fatalf("single-row consolidation reported pairs: %v", stats.ConsolidatedIDs)
	}
}

// A staged inpx:v2 group without same-file v1 rows follows the normal
// new-book path and reports no consolidation pairs.
func TestPublishLibraryInpxV2WithoutV1MatchInsertsNew(t *testing.T) {
	st, ctx := snapshotStore(t)
	libraryID := snapshotLibrary(t, st, ctx, "fresh-v2")
	group := snapshotEntry("inpx:v2:fresh", "Fresh group")
	group.book.File = "fresh"
	snap := newTestSnapshot(t, ctx, group)
	stats, err := st.PublishLibrary(ctx, libraryID, snap, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 1 || stats.Changed != 0 || stats.ConsolidatedIDs != nil {
		t.Fatalf("no-match v2 stats = %+v (pairs %v), want one plain insert", stats, stats.ConsolidatedIDs)
	}
	if !snapshotSearchIndexed(t, st, snapshotBookID(t, st, libraryID, "inpx:v2:fresh")) {
		t.Fatal("new v2 group is missing from the search index")
	}

	// A v1 row of a different file is not a consolidation candidate.
	other := snapshotEntry("inpx:v1:other-file", "Other file")
	other.book.File = "other"
	if err := publishTestSnapshot(t, st, ctx, libraryID, group, other); err != nil {
		t.Fatal(err)
	}
	moved := snapshotEntry("inpx:v2:moved", "Moved group")
	moved.book.File = "elsewhere"
	snap = newTestSnapshot(t, ctx, group, other, moved)
	stats, err = st.PublishLibrary(ctx, libraryID, snap, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 1 || stats.Changed != 0 || stats.Unchanged != 2 || stats.ConsolidatedIDs != nil {
		t.Fatalf("different-file v2 stats = %+v (pairs %v), want one plain insert", stats, stats.ConsolidatedIDs)
	}
}

// Same-name files in different formats are different books: consolidation
// binds by exact (folder, file, ext) and never crosses formats.
func TestPublishLibraryConsolidationKeepsFormatsSeparate(t *testing.T) {
	st, ctx := snapshotStore(t)
	libraryID := snapshotLibrary(t, st, ctx, "formats")

	fb2 := snapshotEntry("inpx:v1:fmt-fb2", "Same name")
	fb2.book.File = "dup"
	epub := snapshotEntry("inpx:v1:fmt-epub", "Same name")
	epub.book.File = "dup"
	epub.book.Ext = "epub"
	if err := publishTestSnapshot(t, st, ctx, libraryID, fb2, epub); err != nil {
		t.Fatal(err)
	}
	fb2ID := snapshotBookID(t, st, libraryID, "inpx:v1:fmt-fb2")
	epubID := snapshotBookID(t, st, libraryID, "inpx:v1:fmt-epub")

	fb2Group := snapshotEntry("inpx:v2:fmt-fb2", "Same name")
	fb2Group.book.File = "dup"
	epubGroup := snapshotEntry("inpx:v2:fmt-epub", "Same name")
	epubGroup.book.File = "dup"
	epubGroup.book.Ext = "epub"
	snap := newTestSnapshot(t, ctx, fb2Group, epubGroup)
	stats, err := st.PublishLibrary(ctx, libraryID, snap, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 0 || stats.Changed != 2 || stats.Removed != 0 || len(stats.ConsolidatedIDs) != 0 {
		t.Fatalf("format consolidation stats = %+v (pairs %v), want two in-place rekeys", stats, stats.ConsolidatedIDs)
	}
	if got := snapshotBookID(t, st, libraryID, "inpx:v2:fmt-fb2"); got != fb2ID {
		t.Fatalf("fb2 group rebound to ID %d, want the fb2 row %d", got, fb2ID)
	}
	if got := snapshotBookID(t, st, libraryID, "inpx:v2:fmt-epub"); got != epubID {
		t.Fatalf("epub group rebound to ID %d, want the epub row %d", got, epubID)
	}
}

// A single-member group whose member hash equals the winner's v1 key is a
// pure key-swap: no content rewrite, so metadata learned after the original
// import (ISBNs, reader-added series) survives the consolidation.
func TestPublishLibraryConsolidationKeySwapPreservesContent(t *testing.T) {
	st, ctx := snapshotStore(t)
	libraryID := snapshotLibrary(t, st, ctx, "keyswap")

	solo := snapshotEntry("inpx:v1:aaa", "Solo book")
	solo.book.File = "solo"
	solo.book.Series = "Indexed"
	solo.book.SeriesNum = 1
	if err := publishTestSnapshot(t, st, ctx, libraryID, solo); err != nil {
		t.Fatal(err)
	}
	winnerID := snapshotBookID(t, st, libraryID, "inpx:v1:aaa")
	if _, err := st.DB().Exec(`UPDATE books SET isbn='9781234567890' WHERE id=?`, winnerID); err != nil {
		t.Fatal(err)
	}
	if err := st.AddBookSeries(ctx, winnerID, []SeriesRef{{Title: "Learned", SeqNumber: 4}}); err != nil {
		t.Fatal(err)
	}

	group := snapshotEntry("inpx:v2:solo", "Solo book")
	group.book.File = "solo"
	group.book.Series = "Indexed"
	group.book.SeriesNum = 1
	snap, err := NewSnapshot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = snap.Close() })
	if err := snap.add(ctx, "inpx:v2:solo", group.book, `["aaa"]`); err != nil {
		t.Fatal(err)
	}
	stats, err := st.PublishLibrary(ctx, libraryID, snap, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Changed != 1 || stats.Added != 0 || len(stats.ConsolidatedIDs) != 0 {
		t.Fatalf("key-swap stats = %+v, want one rekey and no losers", stats)
	}

	// Identity moves, content and learned metadata stay exactly as they were.
	if got := snapshotBookID(t, st, libraryID, "inpx:v2:solo"); got != winnerID {
		t.Fatalf("key-swap rebound ID = %d, want %d", got, winnerID)
	}
	var title, isbn string
	if err := st.DB().QueryRow(`SELECT title, isbn FROM books WHERE id=?`, winnerID).Scan(&title, &isbn); err != nil {
		t.Fatal(err)
	}
	if title != "Solo book" || isbn != "9781234567890" {
		t.Fatalf("key-swap row = %q isbn %q, want untouched content", title, isbn)
	}
	refs, err := st.BookSeries(ctx, winnerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("key-swap series = %+v, want indexed and learned entries", refs)
	}
	if !snapshotSearchIndexed(t, st, winnerID) {
		t.Fatal("key-swap winner lost its search row")
	}
}

// A single-member group whose member hash differs from the winner's v1 key
// carries changed metadata: the full rewrite path applies it.
func TestPublishLibraryConsolidationRewriteOnChangedMember(t *testing.T) {
	st, ctx := snapshotStore(t)
	libraryID := snapshotLibrary(t, st, ctx, "rewrite")

	old := snapshotEntry("inpx:v1:aaa", "Old title")
	old.book.File = "changed"
	if err := publishTestSnapshot(t, st, ctx, libraryID, old); err != nil {
		t.Fatal(err)
	}
	winnerID := snapshotBookID(t, st, libraryID, "inpx:v1:aaa")

	group := snapshotEntry("inpx:v2:changed", "New title")
	group.book.File = "changed"
	snap, err := NewSnapshot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = snap.Close() })
	if err := snap.add(ctx, "inpx:v2:changed", group.book, `["bbb"]`); err != nil {
		t.Fatal(err)
	}
	stats, err := st.PublishLibrary(ctx, libraryID, snap, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Changed != 1 || stats.Added != 0 {
		t.Fatalf("changed-member stats = %+v, want one rekey", stats)
	}
	var title string
	if err := st.DB().QueryRow(`SELECT title FROM books WHERE id=?`, winnerID).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "New title" {
		t.Fatalf("changed-member title = %q, want the staged metadata", title)
	}
}

func TestPublishLibraryRejectsExhaustedBookIDRange(t *testing.T) {
	st, ctx := snapshotStore(t)
	libraryID := snapshotLibrary(t, st, ctx, "exhausted-ids")
	const maxBookID = math.MaxInt64
	if _, err := st.DB().Exec(`UPDATE meta SET value=? WHERE key=?`, fmt.Sprint(maxBookID), bookIDHighWaterKey); err != nil {
		t.Fatal(err)
	}
	snapshot := newTestSnapshot(t, ctx, snapshotEntry("record:new", "No ID available"))
	if _, err := st.PublishLibrary(ctx, libraryID, snapshot, false); err == nil {
		t.Fatal("publication succeeded with an exhausted book ID range")
	}
	if _, err := snapshotBookIDNoFail(st, libraryID, "record:new"); err == nil {
		t.Fatal("exhausted ID publication left a book row")
	}
	if got := st.GetMeta(ctx, bookIDHighWaterKey, ""); got != fmt.Sprint(maxBookID) {
		t.Fatalf("high-water mark changed to %q, want %d", got, maxBookID)
	}
}

func TestPublishLibraryReusesExistingEntitiesAndIndexesNewOnes(t *testing.T) {
	st, ctx := snapshotStore(t)
	libraryID := snapshotLibrary(t, st, ctx, "entity-cache")
	first := snapshotEntry("record:first", "First title")
	first.book.Authors = []AuthorName{{Last: "SharedAuthor"}}
	first.book.SeriesList = []SeriesRef{{Title: "SharedSeries", SeqNumber: 1}}
	if err := publishTestSnapshot(t, st, ctx, libraryID, first); err != nil {
		t.Fatal(err)
	}

	second := snapshotEntry("record:second", "Second title")
	second.book.Authors = []AuthorName{{ID: 77, Last: "SharedAuthor"}, {Last: "NewAuthor"}}
	second.book.SeriesList = []SeriesRef{{Title: "SharedSeries", SeqNumber: 2}, {Title: "NewSeries", SeqNumber: 1}}
	if err := publishTestSnapshot(t, st, ctx, libraryID, second); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"authors": 2, "authors_search": 2, "series": 2, "series_search": 2} {
		var got int
		if err := st.DB().QueryRow(`SELECT count(*) FROM ` + table).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Errorf("%s count = %d, want %d", table, got, want)
		}
	}
	secondID := snapshotBookID(t, st, libraryID, "record:second")
	if !snapshotSearchIndexed(t, st, secondID) {
		t.Fatal("new book was not added to full-text search")
	}
}
