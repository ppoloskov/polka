package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/vestigiumincaligne/polka/internal/sqlitedrv"
)

const v5LibraryFixture = `
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE folders (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE);
CREATE TABLE books (
	id INTEGER PRIMARY KEY, lib_id TEXT NOT NULL DEFAULT '', title TEXT NOT NULL,
	series_id INTEGER, series_num INTEGER, folder_id INTEGER NOT NULL REFERENCES folders(id),
	file TEXT NOT NULL, ext TEXT NOT NULL DEFAULT 'fb2', size INTEGER NOT NULL DEFAULT 0,
	lang TEXT NOT NULL DEFAULT '', year INTEGER, added TEXT NOT NULL DEFAULT '',
	lib_rate REAL NOT NULL DEFAULT 0, deleted INTEGER NOT NULL DEFAULT 0,
	file_hash TEXT NOT NULL DEFAULT '', content_hash TEXT NOT NULL DEFAULT '', isbn TEXT NOT NULL DEFAULT ''
);
CREATE TABLE book_authors (book_id INTEGER NOT NULL, author_id INTEGER NOT NULL,
	PRIMARY KEY (book_id, author_id)) WITHOUT ROWID;
CREATE TABLE book_series (book_id INTEGER NOT NULL, series_id INTEGER NOT NULL,
	series_num INTEGER NOT NULL DEFAULT 0, position INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (book_id, series_id)) WITHOUT ROWID;
CREATE TABLE book_digests (digest TEXT PRIMARY KEY, book_id INTEGER NOT NULL) WITHOUT ROWID;
INSERT INTO meta VALUES ('collection_name', 'Old shelf');
INSERT INTO folders VALUES (1, 'old.zip');
INSERT INTO books (id, title, folder_id, file, deleted) VALUES (41, 'Old book', 1, '41', 1);
INSERT INTO book_authors VALUES (41, 7);
INSERT INTO book_series VALUES (41, 9, 3, 0);
INSERT INTO book_digests VALUES ('digest-77', 77);
PRAGMA user_version = 5;
`

func openV5Fixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "polka.db")
	db, err := sqlitedrv.Open(path, sqlitedrv.Options{WAL: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(v5LibraryFixture); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMigrateV5CreatesLegacyLibraryWithoutChangingCatalog(t *testing.T) {
	path := openV5Fixture(t)
	for i := 0; i < 2; i++ {
		st, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		var title string
		var deleted int
		if err := st.DB().QueryRow(`SELECT title, deleted FROM books WHERE id=41`).Scan(&title, &deleted); err != nil || title != "Old book" || deleted != 1 {
			t.Fatalf("legacy book = %q deleted=%d err=%v", title, deleted, err)
		}
		var authorID, seriesID, digestBookID int64
		if err := st.DB().QueryRow(`SELECT author_id FROM book_authors WHERE book_id=41`).Scan(&authorID); err != nil || authorID != 7 {
			t.Fatalf("author relation = %d, %v", authorID, err)
		}
		if err := st.DB().QueryRow(`SELECT series_id FROM book_series WHERE book_id=41`).Scan(&seriesID); err != nil || seriesID != 9 {
			t.Fatalf("series relation = %d, %v", seriesID, err)
		}
		if err := st.DB().QueryRow(`SELECT book_id FROM book_digests WHERE digest='digest-77'`).Scan(&digestBookID); err != nil || digestBookID != 77 {
			t.Fatalf("digest reference = %d, %v", digestBookID, err)
		}
		if err := st.ReserveBookIDs(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if got := st.GetMeta(ctx, bookIDHighWaterKey, ""); got != "77" {
			t.Fatalf("lower reservation reduced allocator floor to %q", got)
		}
		if got := st.GetMeta(ctx, "collection_name", ""); got != "Old shelf" {
			t.Fatalf("collection_name = %q", got)
		}
		libs, err := st.ListLibraries(ctx)
		if err != nil || len(libs) != 1 || libs[0].ID != "legacy" || libs[0].Name != "Old shelf" || libs[0].Kind != "legacy" || !libs[0].Enabled {
			t.Fatalf("libraries = %+v, %v", libs, err)
		}
		var libraryID, sourceKey string
		var missing int
		if err := st.DB().QueryRow(`SELECT library_id, source_key, missing FROM books WHERE id=41`).Scan(&libraryID, &sourceKey, &missing); err != nil || libraryID != "legacy" || sourceKey != "" || missing != 0 {
			t.Fatalf("ownership = %q key=%q missing=%d err=%v", libraryID, sourceKey, missing, err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLibraryRegistryAndBookIDHighWater(t *testing.T) {
	path := filepath.Join(t.TempDir(), "polka.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initial, err := st.ListLibraries(ctx)
	if err != nil || len(initial) != 1 || initial[0].ID != "legacy" {
		t.Fatalf("fresh libraries = %+v, %v", initial, err)
	}
	added, err := st.AddLibrary(ctx, Library{Name: "Personal", Kind: "folder", Root: "/books", Enabled: true})
	if err != nil || added.ID == "" || added.Name != "Personal" {
		t.Fatalf("AddLibrary = %+v, %v", added, err)
	}
	got, err := st.Library(ctx, added.ID)
	if err != nil || *got != *added {
		t.Fatalf("Library = %+v, %v; want %+v", got, err, added)
	}
	if _, err := st.AddLibrary(ctx, Library{Name: "", Kind: "folder", Root: "/books"}); err == nil {
		t.Fatal("AddLibrary accepted an empty name")
	}
	for _, invalid := range []Library{
		{Name: "Bad kind", Kind: "legacy", Root: "/books"},
		{Name: "Relative root", Kind: "folder", Root: "books"},
		{Name: "Relative source", Kind: "folder", Root: "/books", SourcePath: "source.inpx"},
		{Name: "Missing source", Kind: "inpx", Root: "/books"},
	} {
		if _, err := st.AddLibrary(ctx, invalid); err == nil {
			t.Errorf("AddLibrary accepted invalid config %+v", invalid)
		}
	}
	if err := st.ReserveBookIDs(ctx, 100); err != nil {
		t.Fatal(err)
	}
	input := &BookInput{Title: "Uploaded", Folder: "uploads", File: "one", Ext: "fb2"}
	id, err := st.AddBook(ctx, input)
	if err != nil || id <= 100 {
		t.Fatalf("AddBook ID = %d, %v; want >100", id, err)
	}
	if err := st.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	session, err := st.NewImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Add(&BookInput{Title: "Imported", Folder: "a.zip", File: "two", Ext: "fb2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Finish(); err != nil {
		t.Fatal(err)
	}
	var importedID int64
	if err := st.DB().QueryRow(`SELECT id FROM books WHERE title='Imported'`).Scan(&importedID); err != nil || importedID <= id {
		t.Fatalf("import ID = %d, %v; want > prior upload ID %d", importedID, err, id)
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "newer.db")
	db, err := sqlitedrv.Open(path, sqlitedrv.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 999`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if st, err := Open(path); err == nil {
		st.Close()
		t.Fatal("Open accepted a database from a newer schema")
	}
}
