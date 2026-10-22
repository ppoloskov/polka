package store

import (
	"context"
	"testing"
	"time"
)

func TestLibraryReadsRemainAvailableDuringUncommittedPublication(t *testing.T) {
	st, ids := newLibrary(t)
	ctx := context.Background()
	libraryID := "legacy"
	booksBefore, err := st.BookCount(ctx)
	if err != nil || booksBefore == 0 {
		t.Fatalf("initial BookCount = %d, %v", booksBefore, err)
	}

	// Hold the same write transaction used by publication after changing its
	// catalog rows. Readers must continue seeing the previous commit.
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE libraries SET name = 'Uncommitted' WHERE id = ?`, libraryID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM books WHERE library_id = ?`, libraryID); err != nil {
		t.Fatal(err)
	}

	readCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	if got, err := st.Library(readCtx, libraryID); err != nil || got.Name != "Library" {
		t.Fatalf("Library during publication = %+v, %v; want previous committed row", got, err)
	}
	libraries, err := st.ListLibraries(readCtx)
	if err != nil || len(libraries) == 0 || libraries[0].Name != "Library" {
		t.Fatalf("ListLibraries during publication = %+v, %v; want previous committed row", libraries, err)
	}
	if got, err := st.BookCount(readCtx); err != nil || got != booksBefore {
		t.Fatalf("BookCount during publication = %d, %v; want previous committed count %d", got, err, booksBefore)
	}
	bookID := ids["Война и мир"]
	if got, err := st.BookDetails(readCtx, bookID); err != nil || got.Title != "Война и мир" {
		t.Fatalf("BookDetails during publication = %+v, %v; want previous committed book", got, err)
	}
	books, err := st.SearchTitles(readCtx, "Война", 10)
	if err != nil || len(books) != 1 || books[0].ID != bookID {
		t.Fatalf("SearchTitles during publication = %+v, %v; want previous committed book", books, err)
	}

	// Additional writers queue behind the held writer connection and run after
	// publication commits, rather than opening a concurrent write transaction.
	writeDone := make(chan error, 2)
	started := make(chan struct{}, 2)
	waitCount := st.db.Stats().WaitCount
	go func() {
		started <- struct{}{}
		writeDone <- st.SetMeta(ctx, "concurrent_a", "ok")
	}()
	go func() {
		started <- struct{}{}
		writeDone <- st.SetMeta(ctx, "concurrent_b", "ok")
	}()
	<-started
	<-started
	deadline := time.Now().Add(time.Second)
	for st.db.Stats().WaitCount < waitCount+2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if st.db.Stats().WaitCount < waitCount+2 {
		t.Fatal("writers did not queue behind the held transaction")
	}
	if len(writeDone) != 0 {
		t.Fatal("writer completed before the held transaction committed")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-writeDone; err != nil {
			t.Fatalf("queued write failed: %v", err)
		}
	}
	if got, err := st.Library(ctx, libraryID); err != nil || got.Name != "Uncommitted" {
		t.Fatalf("Library after publication commit = %+v, %v; want committed row", got, err)
	}
}
