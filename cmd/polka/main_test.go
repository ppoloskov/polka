package main

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/vestigiumincaligne/polka/internal/auth"
	"github.com/vestigiumincaligne/polka/internal/store"
)

func TestRunImportReservesExistingUserBookIDs(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "polka.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddBook(context.Background(), &store.BookInput{Title: "Old catalog", Folder: "old", File: "old", Ext: "fb2"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	users, err := auth.Open(filepath.Join(dir, "users.db"))
	if err != nil {
		t.Fatal(err)
	}
	user, err := users.CreateUser(context.Background(), "reader", "secret123", "", auth.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := users.SaveProgress(context.Background(), user.ID, 100, auth.Progress{Chapter: 1}); err != nil {
		t.Fatal(err)
	}
	if err := users.Close(); err != nil {
		t.Fatal(err)
	}

	var data bytes.Buffer
	z := zip.NewWriter(&data)
	w, err := z.Create("books.inp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("Chekhov,Anton,\x04prose_classic\x04Ward No. 6\x04\x04\x04ward6\x04500\x041\x040\x04fb2\x042024-01-01\x04en\x045\x04\x041892\x04src\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	inpx := filepath.Join(dir, "catalog.inpx")
	if err := os.WriteFile(inpx, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := runImport(log, []string{"--data-dir", dir, "--inpx", inpx, "--replace"}); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(filepath.Join(dir, "polka.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var bookID int64
	if err := st.DB().QueryRow(`SELECT id FROM books WHERE title = 'Ward No. 6'`).Scan(&bookID); err != nil {
		t.Fatal(err)
	}
	if bookID <= 100 {
		t.Fatalf("imported book ID = %d; want >100 to preserve the existing user reference", bookID)
	}
}
