package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vestigiumincaligne/polka/internal/library"
	"github.com/vestigiumincaligne/polka/internal/store"
)

const folderFB2 = `<?xml version="1.0" encoding="UTF-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info><genre>sf</genre>
<author><first-name>Лев</first-name><last-name>Автор</last-name></author>
<book-title>Папка с книгами</book-title><lang>ru</lang>
<sequence name="Цикл" number="2"/></title-info></description>
<body><section><p>Текст.</p></section></body></FictionBook>`

func folderLibrary(t *testing.T, st *store.Store, name, root string) string {
	t.Helper()
	lib, err := st.AddLibrary(context.Background(), store.Library{
		Name: name, Kind: "folder", Root: root, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return lib.ID
}

func publishFolder(t *testing.T, st *store.Store, libraryID, root string) error {
	t.Helper()
	snapshot, err := StageFolder(context.Background(), root, t.TempDir(), nil)
	if err != nil {
		return err
	}
	defer snapshot.Close()
	_, err = st.PublishLibrary(context.Background(), libraryID, snapshot, false)
	return err
}

type folderBook struct {
	id       int64
	title    string
	ext      string
	path     string
	added    string
	fileHash string
	key      string
	missing  int
}

func folderBooks(t *testing.T, st *store.Store, libraryID string) map[string]folderBook {
	t.Helper()
	rows, err := st.DB().Query(`
		SELECT b.id, b.title, f.name, b.file, b.ext, b.added, b.file_hash, b.source_key, b.missing
		FROM books b JOIN folders f ON f.id=b.folder_id
		WHERE b.library_id=?`, libraryID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := make(map[string]folderBook)
	for rows.Next() {
		var b folderBook
		var dir, file, ext string
		if err := rows.Scan(&b.id, &b.title, &dir, &file, &ext, &b.added, &b.fileHash, &b.key, &b.missing); err != nil {
			t.Fatal(err)
		}
		b.ext = ext
		b.path = filepath.ToSlash(filepath.Join(dir, file+"."+ext))
		if current, ok := out[b.path]; !ok || current.missing != 0 {
			out[b.path] = b
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestStageFolderPublishesNestedFilesWithStablePathContentIdentity(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	root := t.TempDir()
	otherRoot := t.TempDir()
	libraryID := folderLibrary(t, st, "folder", root)
	otherID := folderLibrary(t, st, "other", otherRoot)
	otherBookID, err := st.AddBook(ctx, &store.BookInput{
		LibraryID: otherID, SourceKey: "other:stable", Title: "Other library", Folder: ".", File: "other", Ext: "txt",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{"nested", "copies/a", "copies/b"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"nested/reader.fb2": folderFB2,
		"nested/UPPER.FB2":  folderFB2,
		"nested/note.txt":   "A plain text book.\n",
		"copies/a/copy.txt": "identical copy\n",
		"copies/b/copy.txt": "identical copy\n",
		"ignored.bin":       "not a supported upload type",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "escaped.txt"), []byte("outside directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(root, "linked-dir")); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}

	if err := publishFolder(t, st, libraryID, root); err != nil {
		t.Fatal(err)
	}
	books := folderBooks(t, st, libraryID)
	if len(books) != 5 {
		t.Fatalf("published files = %+v; want 5 supported regular files", books)
	}
	fb2, ok := books["nested/reader.fb2"]
	if !ok || fb2.title != "Папка с книгами" {
		t.Fatalf("FB2 metadata = %+v, present %v", fb2, ok)
	}
	upper := books["nested/UPPER.FB2"]
	if upper.ext != "FB2" {
		t.Fatalf("uppercase FB2 extension = %q; want original filename case", upper.ext)
	}
	rc, _, err := library.New(root).Open("nested", "UPPER", upper.ext)
	if err != nil {
		t.Fatalf("open uppercase FB2 locator: %v", err)
	}
	opened, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || string(opened) != folderFB2 {
		t.Fatalf("uppercase FB2 contents = %q, %v", opened, err)
	}
	var author string
	if err := st.DB().QueryRow(`SELECT a.last_name FROM book_authors ba JOIN authors a ON a.id=ba.author_id WHERE ba.book_id=?`, fb2.id).Scan(&author); err != nil || author != "Автор" {
		t.Fatalf("FB2 author = %q, %v", author, err)
	}
	var series string
	if err := st.DB().QueryRow(`SELECT s.title FROM books b JOIN series s ON s.id=b.series_id WHERE b.id=?`, fb2.id).Scan(&series); err != nil || series != "Цикл" {
		t.Fatalf("FB2 series = %q, %v", series, err)
	}
	if note := books["nested/note.txt"]; !strings.EqualFold(note.title, "note") {
		t.Errorf("TXT title = %q; want filename fallback", note.title)
	}
	a, aOK := books["copies/a/copy.txt"]
	b, bOK := books["copies/b/copy.txt"]
	if !aOK || !bOK || a.id == b.id || a.key == b.key {
		t.Fatalf("identical copies = %+v, %+v; want distinct path identities", a, b)
	}
	if !strings.Contains(a.key, "copies/a/copy.txt") {
		t.Errorf("source key %q does not include relative path", a.key)
	}
	for rel, contents := range files {
		if strings.HasSuffix(rel, ".bin") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || string(data) != contents {
			t.Fatalf("source changed at %s: %q, %v", rel, data, err)
		}
	}
	if got, err := st.BookFile(ctx, otherBookID); err != nil || got.Title != "Other library" {
		t.Fatalf("other library book after publish = %+v, %v", got, err)
	}

	firstID, firstAdded := a.id, a.added
	copySum := sha256.Sum256([]byte("identical copy\n"))
	if firstAdded == "" || a.fileHash != hex.EncodeToString(copySum[:]) {
		t.Fatalf("initial source fields = added %q file_hash %q; want date and full SHA-256", firstAdded, a.fileHash)
	}
	if err := publishFolder(t, st, libraryID, root); err != nil {
		t.Fatal(err)
	}
	books = folderBooks(t, st, libraryID)
	if repeated := books["copies/a/copy.txt"]; repeated.id != firstID || repeated.added != firstAdded || repeated.fileHash != a.fileHash {
		t.Fatalf("unchanged copy = %+v; want stable ID %d, added %q, and file hash %q", repeated, firstID, firstAdded, a.fileHash)
	}

	changedPath := filepath.Join(root, "nested", "note.txt")
	if err := os.WriteFile(changedPath, []byte("Changed plain text.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishFolder(t, st, libraryID, root); err != nil {
		t.Fatal(err)
	}
	changed := folderBooks(t, st, libraryID)
	oldNote := books["nested/note.txt"]
	newNote := changed["nested/note.txt"]
	var oldMissing int
	if err := st.DB().QueryRow(`SELECT missing FROM books WHERE id=?`, oldNote.id).Scan(&oldMissing); err != nil || oldMissing != 1 {
		t.Fatalf("changed source old row missing = %d, %v; want 1", oldMissing, err)
	}
	if newNote.id == oldNote.id || newNote.key == oldNote.key {
		t.Fatalf("changed source reused identity: old %+v, new %+v", oldNote, newNote)
	}
	if err := os.MkdirAll(filepath.Join(root, "copies", "moved"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "copies", "a", "copy.txt"), filepath.Join(root, "copies", "moved", "copy.txt")); err != nil {
		t.Fatal(err)
	}
	if err := publishFolder(t, st, libraryID, root); err != nil {
		t.Fatal(err)
	}
	moved := folderBooks(t, st, libraryID)
	if moved["copies/moved/copy.txt"].id == firstID || moved["copies/b/copy.txt"].id != b.id {
		t.Fatalf("moved/duplicate identities = moved %+v, unchanged %+v; want new moved ID and stable duplicate %d", moved["copies/moved/copy.txt"], moved["copies/b/copy.txt"], b.id)
	}
	if err := st.DB().QueryRow(`SELECT missing FROM books WHERE id=?`, firstID).Scan(&oldMissing); err != nil || oldMissing != 1 {
		t.Fatalf("moved source old row missing = %d, %v; want 1", oldMissing, err)
	}
	if !strings.Contains(a.key, hex.EncodeToString(copySum[:])) {
		t.Errorf("source key %q does not include full content SHA-256 %s", a.key, hex.EncodeToString(copySum[:]))
	}
	if gotID, err := st.LibraryForBook(ctx, otherBookID); err != nil || gotID.ID != otherID {
		t.Fatalf("other library ownership after publish = %+v, %v", gotID, err)
	}
}

func TestStageFolderRejectsInvalidRootsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := StageFolder(ctx, t.TempDir(), t.TempDir(), nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled stage error = %v; want context.Canceled", err)
	}
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := StageFolder(context.Background(), file, t.TempDir(), nil); err == nil {
		t.Error("regular file root unexpectedly staged")
	}
	if _, err := StageFolder(context.Background(), filepath.Join(t.TempDir(), "missing"), t.TempDir(), nil); err == nil {
		t.Error("missing root unexpectedly staged")
	}
	if _, err := StageFolder(context.Background(), "", t.TempDir(), nil); err == nil {
		t.Error("empty root unexpectedly staged")
	}
	root := t.TempDir()
	if _, err := StageFolder(context.Background(), root, filepath.Join(root, ".staging"), nil); err == nil {
		t.Error("staging directory inside source root unexpectedly accepted")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can read mode-000 files")
	}
	root = t.TempDir()
	blocked := filepath.Join(root, "blocked.txt")
	if err := os.WriteFile(blocked, []byte("unreadable"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(blocked, 0600)
	if _, err := StageFolder(context.Background(), root, t.TempDir(), nil); err == nil {
		t.Error("unreadable file unexpectedly staged")
	}
}
