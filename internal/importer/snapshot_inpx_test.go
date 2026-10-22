package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	if !strings.HasPrefix(firstKey, "inpx:v2:") {
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

func TestStageInpxRejectsIncompleteAndCorruptSources(t *testing.T) {
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
		"incomplete row":    badRow,
		"truncated archive": truncated,
		"corrupt archive":   corrupt,
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

// Flibusta repeats a multi-series file as one line per series; the lines of
// one (folder, file, ext) collapse into a single staged group and book.
func TestStageInpxCollapsesPerSeriesLinesOfOneFile(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	libraryID := inpxLibrary(t, st, ctx, "grouped")
	alpha := record("Author,One,:Shared,Second,", "genre", "A title", "Alpha", "1", "same-locator", "fb2")
	beta := record("Author,One,", "genre:other", "A title", "Beta", "2", "same-locator", "fb2")
	other := record("Author,Two,", "genre", "Another title", "", "", "other-locator", "fb2")
	source := inpxSource(t, filepath.Join(t.TempDir(), "grouped"), alpha, beta, other)

	snap, info, err := StageInpxWithInfo(ctx, source, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.Stats.Books != 2 {
		t.Fatalf("staged groups = %d, want 2 (one file group plus one single-line file)", info.Stats.Books)
	}
	if info.Stats.Authors != 3 || info.Stats.Series != 2 || info.Stats.Genres != 2 {
		t.Fatalf("staged stats = %+v", info.Stats)
	}
	if _, err := st.PublishLibrary(ctx, libraryID, snap, false); err != nil {
		t.Fatal(err)
	}
	_ = snap.Close()

	var count int
	if err := st.DB().QueryRow(`SELECT count(*) FROM books WHERE library_id=? AND file=?`, libraryID, "same-locator").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rows for one file = %d, want 1", count)
	}
	var key, title, primarySeries string
	var primaryNum int
	if err := st.DB().QueryRow(`
		SELECT b.source_key, b.title, coalesce(s.title, ''), coalesce(b.series_num, 0)
		FROM books b LEFT JOIN series s ON s.id = b.series_id
		WHERE b.library_id=? AND b.file=?`, libraryID, "same-locator").Scan(&key, &title, &primarySeries, &primaryNum); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "inpx:v2:") {
		t.Fatalf("group source key = %q, want inpx:v2: prefix", key)
	}
	if title != "A title" || primarySeries != "Alpha" || primaryNum != 1 {
		t.Fatalf("merged base fields = %q / %q #%d, want first line's A title / Alpha #1", title, primarySeries, primaryNum)
	}
	id := inpxBookID(t, st, libraryID, key)
	refs, err := st.BookSeries(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].Title != "Alpha" || refs[0].SeqNumber != 1 || refs[1].Title != "Beta" || refs[1].SeqNumber != 2 {
		t.Fatalf("merged series = %+v, want Alpha #1 then Beta #2", refs)
	}
	var authorCount int
	if err := st.DB().QueryRow(`SELECT count(*) FROM book_authors WHERE book_id=?`, id).Scan(&authorCount); err != nil {
		t.Fatal(err)
	}
	if authorCount != 2 {
		t.Fatalf("merged authors = %d, want deduped union of 2", authorCount)
	}
	var genreCount int
	if err := st.DB().QueryRow(`SELECT count(*) FROM book_genres WHERE book_id=?`, id).Scan(&genreCount); err != nil {
		t.Fatal(err)
	}
	if genreCount != 2 {
		t.Fatalf("merged genres = %d, want union of 2", genreCount)
	}

	// Byte-identical duplicate lines of one file collapse the same way.
	duplicate := inpxSource(t, filepath.Join(t.TempDir(), "duplicate"), other, other)
	if err := publishInpx(t, st, ctx, libraryID, duplicate); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT count(*) FROM books WHERE library_id=? AND file=? AND missing=0`, libraryID, "other-locator").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rows after duplicate-line source = %d, want 1", count)
	}
}

// The group fingerprint covers the sorted member hashes, so reordering the
// index lines of a file keeps the staged key stable.
func TestStageInpxGroupKeyStableAcrossLineOrder(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	alpha := record("Author,One,", "genre", "A title", "Alpha", "1", "same-locator", "fb2")
	beta := record("Author,One,", "genre", "A title", "Beta", "2", "same-locator", "fb2")
	keyFor := func(name string, rows ...string) string {
		t.Helper()
		libraryID := inpxLibrary(t, st, ctx, name)
		if err := publishInpx(t, st, ctx, libraryID, inpxSource(t, filepath.Join(t.TempDir(), name), rows...)); err != nil {
			t.Fatal(err)
		}
		var key string
		if err := st.DB().QueryRow(`SELECT source_key FROM books WHERE library_id=?`, libraryID).Scan(&key); err != nil {
			t.Fatal(err)
		}
		return key
	}
	forward := keyFor("forward", alpha, beta)
	swapped := keyFor("swapped", beta, alpha)
	if forward != swapped {
		t.Fatalf("group key changed with line order: %q vs %q", forward, swapped)
	}
}

// A file group maps to missing only when every one of its lines is deleted.
func TestStageInpxGroupDeletedOnlyWhenAllLinesDeleted(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	libraryID := inpxLibrary(t, st, ctx, "deleted-group")
	alpha := record("Author,One,", "genre", "A title", "Alpha", "1", "locator", "fb2")
	beta := record("Author,One,", "genre", "A title", "Beta", "2", "locator", "fb2")
	deleted := func(row string) string {
		fields := strings.Split(row, sep)
		fields[8] = "1"
		return strings.Join(fields, sep)
	}

	if err := publishInpx(t, st, ctx, libraryID, inpxSource(t, filepath.Join(t.TempDir(), "live"), alpha, beta)); err != nil {
		t.Fatal(err)
	}
	var key string
	if err := st.DB().QueryRow(`SELECT source_key FROM books WHERE library_id=?`, libraryID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	id := inpxBookID(t, st, libraryID, key)

	if err := publishInpx(t, st, ctx, libraryID, inpxSource(t, filepath.Join(t.TempDir(), "mixed"), alpha, deleted(beta))); err != nil {
		t.Fatal(err)
	}
	gotID, missing, _ := inpxState(t, st, libraryID, key)
	if gotID != id || missing != 0 {
		t.Fatalf("partly deleted group = id %d missing %d, want id %d missing 0", gotID, missing, id)
	}

	if err := publishInpx(t, st, ctx, libraryID, inpxSource(t, filepath.Join(t.TempDir(), "deleted"), deleted(alpha), deleted(beta))); err != nil {
		t.Fatal(err)
	}
	gotID, missing, _ = inpxState(t, st, libraryID, key)
	if gotID != id || missing != 1 {
		t.Fatalf("fully deleted group = id %d missing %d, want id %d missing 1", gotID, missing, id)
	}
}

func TestInpxSourceKeyV2Golden(t *testing.T) {
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
	hash, err := inpxLineHash(book)
	if err != nil {
		t.Fatal(err)
	}
	const wantHash = "7c26b7c9400a0995112dd0ecff6339b052abe8f6dc24aa9eb0aa8e36c6f4d23b"
	if hash != wantHash {
		t.Fatalf("INPX line hash = %q, want %q", hash, wantHash)
	}
	key, _, err := mergeInpxLines([]store.StagedLine{{Hash: hash, Book: *book}})
	if err != nil {
		t.Fatal(err)
	}
	const want = "inpx:v2:e23e4f31c13731fdb4daf4e4360fc95a4ad626437fcbe69c34e4775a6dd30855"
	if key != want {
		t.Fatalf("INPX v2 source key = %q, want %q", key, want)
	}
}

func TestUpdateInpxStoresPerLibraryProvenanceAndRollsItBackOnPublishFailure(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	libraryID := inpxLibrary(t, st, ctx, "named")
	source := inpxSource(t, filepath.Join(t.TempDir(), "named-source"), record("Author,One,", "genre", "Named title", "", "", "locator", "fb2"))
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(data)
	_, _, err = UpdateInpx(ctx, nil, st, libraryID, source, t.TempDir(), "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	key := "library." + libraryID + ".inpx_sha256"
	if got := st.GetMeta(ctx, key, ""); got != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("named source hash = %q, want %x", got, wantHash)
	}
	if got := st.GetMeta(ctx, "library."+libraryID+".inpx_filename", ""); got != filepath.Base(source) {
		t.Fatalf("named source filename = %q, want %q", got, filepath.Base(source))
	}
	if got := st.GetMeta(ctx, "inpx_sha256", ""); got != "" {
		t.Fatalf("named source overwrote legacy hash alias: %q", got)
	}
	lib, err := st.Library(ctx, libraryID)
	if err != nil {
		t.Fatal(err)
	}
	absSource, err := filepath.Abs(source)
	if err != nil {
		t.Fatal(err)
	}
	if lib.SourcePath != absSource {
		t.Fatalf("named source path = %q, want absolute %q", lib.SourcePath, absSource)
	}
	priorSourcePath := lib.SourcePath
	var priorHighwater string
	if err := st.DB().QueryRow(`SELECT value FROM meta WHERE key='book_id_high_water'`).Scan(&priorHighwater); err != nil {
		t.Fatal(err)
	}

	failed := inpxSource(t, filepath.Join(t.TempDir(), "failed-source"), record("Author,One,", "genre", "Must roll back", "", "", "new-locator", "fb2"))
	if _, err := st.DB().Exec(`CREATE TRIGGER reject_source_path BEFORE UPDATE OF source_path ON libraries BEGIN SELECT RAISE(ABORT, 'source path rejected'); END`); err != nil {
		t.Fatal(err)
	}
	snap, info, err := StageInpxWithInfo(ctx, failed, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	snap.SetInpxSource(filepath.Base(failed), info.SHA256, info.CollectionName, failed, false)
	_, err = st.PublishLibrary(ctx, libraryID, snap, false)
	snap.Close()
	if err == nil {
		t.Fatal("publish unexpectedly succeeded despite source-path trigger")
	}
	var books int
	if err := st.DB().QueryRow(`SELECT count(*) FROM books WHERE library_id=?`, libraryID).Scan(&books); err != nil {
		t.Fatal(err)
	}
	if books != 1 {
		t.Fatalf("books after failed publish = %d, want 1", books)
	}
	if got := st.GetMeta(ctx, key, ""); got != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("named source hash after failed publish = %q, want prior %x", got, wantHash)
	}
	lib, err = st.Library(ctx, libraryID)
	if err != nil || lib.SourcePath != priorSourcePath {
		t.Fatalf("source path after failed publish = %+v, %v; want %q", lib, err, priorSourcePath)
	}
	var highwater string
	if err := st.DB().QueryRow(`SELECT value FROM meta WHERE key='book_id_high_water'`).Scan(&highwater); err != nil {
		t.Fatal(err)
	}
	if highwater != priorHighwater {
		t.Fatalf("book ID high-water after failed publish = %q, want %q", highwater, priorHighwater)
	}
}
