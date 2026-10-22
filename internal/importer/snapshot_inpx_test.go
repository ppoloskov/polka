package importer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vestigiumincaligne/polka/internal/store"
)

func inpxSource(t *testing.T, dir string, rows ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return makeInpx(t, dir, "", map[string][]string{"books.inp": rows})
}

func publishInpx(t *testing.T, st *store.Store, ctx context.Context, libraryID, path string) error {
	t.Helper()
	snap, err := StageInpx(ctx, path, t.TempDir(), nil)
	if err != nil {
		return err
	}
	_, err = st.PublishLibrary(ctx, libraryID, snap, false)
	_ = snap.Close()
	return err
}

func inpxLibrary(t *testing.T, st *store.Store, ctx context.Context, name string) string {
	t.Helper()
	library, err := st.AddLibrary(ctx, store.Library{
		Name: name, Kind: "inpx", Root: filepath.Join(t.TempDir(), name), SourcePath: filepath.Join(t.TempDir(), name+".inpx"), Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return library.ID
}

func inpxBookID(t *testing.T, st *store.Store, libraryID, key string) int64 {
	t.Helper()
	var id int64
	if err := st.DB().QueryRow(`SELECT id FROM books WHERE library_id=? AND source_key=?`, libraryID, key).Scan(&id); err != nil {
		t.Fatalf("book id for %q/%q: %v", libraryID, key, err)
	}
	return id
}

func inpxState(t *testing.T, st *store.Store, libraryID, key string) (int64, int, int) {
	t.Helper()
	var id int64
	var missing, deleted int
	if err := st.DB().QueryRow(`SELECT id, missing, deleted FROM books WHERE library_id=? AND source_key=?`, libraryID, key).Scan(&id, &missing, &deleted); err != nil {
		t.Fatalf("book state for %q/%q: %v", libraryID, key, err)
	}
	return id, missing, deleted
}

func TestStageInpxUsesExactRecordIdentityScopedToLibrary(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	one := inpxLibrary(t, st, ctx, "one")
	two := inpxLibrary(t, st, ctx, "two")

	row := record("Author,One,", "genre", "A title", "", "", "same-locator", "fb2")
	otherFile := record("Author,Two,", "genre", "Another title", "", "", "other-locator", "fb2")
	firstSource := inpxSource(t, filepath.Join(t.TempDir(), "first"), row, otherFile)
	if err := publishInpx(t, st, ctx, one, firstSource); err != nil {
		t.Fatal(err)
	}
	firstKey := ""
	if err := st.DB().QueryRow(`SELECT source_key FROM books WHERE library_id=? AND title=?`, one, "A title").Scan(&firstKey); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(firstKey, "inpx:v1:") {
		t.Fatalf("source key = %q, want versioned INPX prefix", firstKey)
	}
	firstID := inpxBookID(t, st, one, firstKey)

	// The same INPX LibID is valid in another source, and source keys are scoped
	// by the library when the publisher allocates public catalog IDs.
	secondSource := inpxSource(t, filepath.Join(t.TempDir(), "second"), row, otherFile)
	if err := publishInpx(t, st, ctx, two, secondSource); err != nil {
		t.Fatal(err)
	}
	secondID := inpxBookID(t, st, two, firstKey)
	if secondID == firstID {
		t.Fatalf("same source key in separate libraries reused public ID %d", firstID)
	}

	if err := publishInpx(t, st, ctx, one, firstSource); err != nil {
		t.Fatal(err)
	}
	if got := inpxBookID(t, st, one, firstKey); got != firstID {
		t.Fatalf("repeat import ID = %d, want stable ID %d", got, firstID)
	}

	// Exact source metadata is part of identity: punctuation changes allocate a
	// new row and retain the old entry as missing instead of fuzzy-matching it.
	changed := inpxSource(t, filepath.Join(t.TempDir(), "changed"), strings.Replace(row, "A title", "A title!", 1), otherFile)
	if err := publishInpx(t, st, ctx, one, changed); err != nil {
		t.Fatal(err)
	}
	var changedKey string
	if err := st.DB().QueryRow(`SELECT source_key FROM books WHERE library_id=? AND title=?`, one, "A title!").Scan(&changedKey); err != nil {
		t.Fatal(err)
	}
	if changedKey == firstKey {
		t.Fatal("metadata correction reused the prior source key")
	}
	if id, missing, _ := inpxState(t, st, one, firstKey); id != firstID || missing != 1 {
		t.Fatalf("old key state = id %d missing %d, want id %d missing 1", id, missing, firstID)
	}
}

func TestStageInpxMapsDeletedFlagToMissingWithoutChangingIdentity(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	libraryID := inpxLibrary(t, st, ctx, "deleted")
	row := record("Author,One,", "genre", "A title", "", "", "locator", "fb2")
	source := inpxSource(t, filepath.Join(t.TempDir(), "source"), row)
	if err := publishInpx(t, st, ctx, libraryID, source); err != nil {
		t.Fatal(err)
	}
	var key string
	if err := st.DB().QueryRow(`SELECT source_key FROM books WHERE library_id=?`, libraryID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	id := inpxBookID(t, st, libraryID, key)

	deleted := strings.Split(row, sep)
	deleted[8] = "1"
	source = inpxSource(t, filepath.Join(t.TempDir(), "deleted"), strings.Join(deleted, sep))
	if err := publishInpx(t, st, ctx, libraryID, source); err != nil {
		t.Fatal(err)
	}
	gotID, missing, _ := inpxState(t, st, libraryID, key)
	if gotID != id || missing != 1 {
		t.Fatalf("deleted source state = id %d missing %d, want id %d missing 1", gotID, missing, id)
	}

	deleted[8] = "0"
	source = inpxSource(t, filepath.Join(t.TempDir(), "restored"), strings.Join(deleted, sep))
	if err := publishInpx(t, st, ctx, libraryID, source); err != nil {
		t.Fatal(err)
	}
	gotID, missing, _ = inpxState(t, st, libraryID, key)
	if gotID != id || missing != 0 {
		t.Fatalf("restored source state = id %d missing %d, want id %d missing 0", gotID, missing, id)
	}
}

func TestStageInpxRejectsIncompleteDuplicateAndCorruptSources(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	libraryID := inpxLibrary(t, st, ctx, "valid")
	row := record("Author,One,", "genre", "Published", "", "", "locator", "fb2")
	valid := inpxSource(t, filepath.Join(t.TempDir(), "valid"), row)
	if err := publishInpx(t, st, ctx, libraryID, valid); err != nil {
		t.Fatal(err)
	}
	var key string
	if err := st.DB().QueryRow(`SELECT source_key FROM books WHERE library_id=?`, libraryID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	id := inpxBookID(t, st, libraryID, key)

	badRow := inpxSource(t, filepath.Join(t.TempDir(), "malformed"), strings.Join(strings.Split(row, sep)[:3], sep))
	duplicate := inpxSource(t, filepath.Join(t.TempDir(), "duplicate"), row, row)
	truncated := filepath.Join(t.TempDir(), "truncated.inpx")
	data, err := os.ReadFile(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(truncated, data[:len(data)/2], 0600); err != nil {
		t.Fatal(err)
	}
	corrupt := filepath.Join(t.TempDir(), "corrupt.inpx")
	if err := os.WriteFile(corrupt, []byte("not a zip archive"), 0600); err != nil {
		t.Fatal(err)
	}

	for name, source := range map[string]string{
		"incomplete row":        badRow,
		"duplicate full record": duplicate,
		"truncated archive":     truncated,
		"corrupt archive":       corrupt,
	} {
		t.Run(name, func(t *testing.T) {
			if err := publishInpx(t, st, ctx, libraryID, source); err == nil {
				t.Fatal("invalid source unexpectedly published")
			}
			gotID, missing, _ := inpxState(t, st, libraryID, key)
			if gotID != id || missing != 0 {
				t.Fatalf("published row after failed stage = id %d missing %d, want id %d missing 0", gotID, missing, id)
			}
		})
	}
}

func TestInpxSourceKeyV1Golden(t *testing.T) {
	// This encoding is a persisted source identity contract; change it only with
	// an explicit key-version migration.
	book := &store.BookInput{
		LibID: "42", LibraryID: "runtime-library", SourceKey: "runtime-key",
		Title: "A Book", Series: "Series", SeriesNum: 2,
		Authors: []store.AuthorName{{Last: "Reader", First: "Ada"}},
		Genres:  []string{"fiction", "science"}, Keywords: []string{"one", "two"},
		Folder: "books.zip", File: "book-1", Ext: "fb2", Size: 1234,
		Lang: "en", Year: 2024, Added: "2024-02-03", Rate: 4.5, Deleted: true,
	}
	got, err := inpxSourceKey(book)
	if err != nil {
		t.Fatal(err)
	}
	const want = "inpx:v1:7c26b7c9400a0995112dd0ecff6339b052abe8f6dc24aa9eb0aa8e36c6f4d23b"
	if got != want {
		t.Fatalf("INPX v1 source key = %q, want %q", got, want)
	}
}
