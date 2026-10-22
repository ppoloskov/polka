package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vestigiumincaligne/polka/internal/auth"
	"github.com/vestigiumincaligne/polka/internal/store"
)

func writeImportUpdateINPX(t *testing.T, path, title string) []byte {
	t.Helper()
	var data bytes.Buffer
	zw := zip.NewWriter(&data)
	collection, err := zw.Create("collection.info")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collection.Write([]byte("Update fixture\n")); err != nil {
		t.Fatal(err)
	}
	books, err := zw.Create("books.inp")
	if err != nil {
		t.Fatal(err)
	}
	fields := []string{"Writer,Example,", "fiction", title, "Series", "1", "source-locator", "100", "1", "0", "fb2", "2024-01-02", "en", "4", "", "2020", "source-id"}
	if _, err := io.WriteString(books, strings.Join(fields, "\x04")+"\r\n"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestRunImportReplacePublishesWithoutLosingLegacyState(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(dir, "polka.db"))
	if err != nil {
		t.Fatal(err)
	}
	oldID, err := st.AddBook(ctx, &store.BookInput{Title: "Unkeyed old catalog", Folder: "old.zip", File: "old", Ext: "fb2"})
	if err != nil {
		t.Fatal(err)
	}
	fileHash := sha256.Sum256([]byte("uploaded book bytes"))
	uploadID, err := st.AddBook(ctx, &store.BookInput{Title: "Uploaded entry", Folder: "uploads", File: "upload", Ext: "fb2", FileHash: hex.EncodeToString(fileHash[:])})
	if err != nil {
		t.Fatal(err)
	}
	named, err := st.AddLibrary(ctx, store.Library{Name: "Named", Kind: "folder", Root: filepath.Join(dir, "named")})
	if err != nil {
		t.Fatal(err)
	}
	namedID, err := st.AddBook(ctx, &store.BookInput{LibraryID: named.ID, SourceKey: "folder:one", Title: "Named library entry", Folder: "books", File: "one", Ext: "fb2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	users, err := auth.Open(filepath.Join(dir, "users.db"))
	if err != nil {
		t.Fatal(err)
	}
	user, err := users.CreateUser(ctx, "reader", "secret123", "", auth.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := users.SaveProgress(ctx, user.ID, oldID, auth.Progress{Chapter: 7, Overall: .4}); err != nil {
		t.Fatal(err)
	}
	if err := users.Close(); err != nil {
		t.Fatal(err)
	}

	inpxPath := filepath.Join(dir, "catalog.inpx")
	goodBytes := writeImportUpdateINPX(t, inpxPath, "Current source book")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	args := []string{"--data-dir", dir, "--inpx", inpxPath, "--replace"}
	if err := runImport(log, args); err != nil {
		t.Fatal(err)
	}
	readStore := func() *store.Store {
		t.Helper()
		st, err := store.Open(filepath.Join(dir, "polka.db"))
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	readUsers := func() *auth.Service {
		t.Helper()
		users, err := auth.Open(filepath.Join(dir, "users.db"))
		if err != nil {
			t.Fatal(err)
		}
		return users
	}
	assertPreserved := func(st *store.Store) int64 {
		t.Helper()
		var importedID int64
		var importedLibrary string
		if err := st.DB().QueryRow(`SELECT id,library_id FROM books WHERE title='Current source book'`).Scan(&importedID, &importedLibrary); err != nil {
			t.Fatalf("current source book: %v", err)
		}
		if importedLibrary != "legacy" {
			t.Errorf("default import target = %q, want legacy", importedLibrary)
		}
		var missing int
		if err := st.DB().QueryRow(`SELECT missing FROM books WHERE id=?`, oldID).Scan(&missing); err != nil || missing != 1 {
			t.Errorf("unkeyed old row missing = %d, %v; want retained as missing", missing, err)
		}
		var gotHash string
		if err := st.DB().QueryRow(`SELECT file_hash FROM books WHERE id=?`, uploadID).Scan(&gotHash); err != nil || gotHash != hex.EncodeToString(fileHash[:]) {
			t.Errorf("uploaded hash = %q, %v", gotHash, err)
		}
		if err := st.DB().QueryRow(`SELECT count(*) FROM books WHERE id=? AND missing=0`, uploadID).Scan(&missing); err != nil || missing != 1 {
			t.Errorf("uploaded entry availability = %d, %v; want present", missing, err)
		}
		if err := st.DB().QueryRow(`SELECT count(*) FROM books WHERE id=? AND library_id=?`, namedID, named.ID).Scan(&missing); err != nil || missing != 1 {
			t.Errorf("named library entry count = %d, %v; want 1", missing, err)
		}
		wantHash := sha256.Sum256(goodBytes)
		if got := st.GetMeta(ctx, "inpx_sha256", ""); got != hex.EncodeToString(wantHash[:]) {
			t.Errorf("successful source fingerprint = %q, want %x", got, wantHash)
		}
		if got := st.GetMeta(ctx, "collection_name", ""); got != "Update fixture" {
			t.Errorf("successful collection name = %q, want Update fixture", got)
		}
		return importedID
	}

	st = readStore()
	firstID := assertPreserved(st)
	if got := st.GetMeta(ctx, "inpx_filename", ""); got != filepath.Base(inpxPath) {
		t.Errorf("successful source filename = %q, want %q", got, filepath.Base(inpxPath))
	}
	st.Close()

	if err := runImport(log, args); err != nil {
		t.Fatal(err)
	}
	st = readStore()
	if got := assertPreserved(st); got != firstID {
		t.Errorf("repeat replacement ID = %d, want stable ID %d", got, firstID)
	}
	priorName := st.GetMeta(ctx, "inpx_filename", "")
	priorHash := st.GetMeta(ctx, "inpx_sha256", "")
	st.Close()

	badPath := filepath.Join(dir, "truncated.inpx")
	if err := os.WriteFile(badPath, []byte("not a complete INPX archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runImport(log, []string{"--data-dir", dir, "--inpx", badPath, "--replace"}); err == nil {
		t.Fatal("replacement with a corrupt INPX succeeded")
	}
	st = readStore()
	defer st.Close()
	if got := assertPreserved(st); got != firstID {
		t.Errorf("failed replacement ID = %d, want prior ID %d", got, firstID)
	}
	if got := st.GetMeta(ctx, "inpx_filename", ""); got != priorName {
		t.Errorf("failed replacement changed source filename to %q, want %q", got, priorName)
	}
	if got := st.GetMeta(ctx, "inpx_sha256", ""); got != priorHash {
		t.Errorf("failed replacement changed source fingerprint to %q, want %q", got, priorHash)
	}
	users = readUsers()
	defer users.Close()
	progress, err := users.GetProgress(ctx, user.ID, oldID)
	if err != nil || progress.Chapter != 7 || progress.Overall != .4 {
		t.Errorf("old unkeyed progress = %+v, %v", progress, err)
	}
}
