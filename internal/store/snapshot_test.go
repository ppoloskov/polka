package store

import (
	"context"
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
