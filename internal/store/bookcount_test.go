package store

import (
	"context"
	"path/filepath"
	"testing"
)

// The cached visible count serves the home/config endpoints: catalog
// mutations must invalidate it eagerly so the UI never shows stale totals.
func TestVisibleBookCountCachesAndInvalidates(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "count.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	count := func() int {
		t.Helper()
		n, err := st.VisibleBookCount(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(); n != 0 {
		t.Fatalf("empty catalog count = %d, want 0", n)
	}

	id, err := st.AddBook(ctx, &BookInput{Title: "Counted", Folder: "f", File: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Fatalf("count after upload = %d, want 1", n)
	}

	if err := st.SetBookDeleted(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Fatalf("count after delete = %d, want 0", n)
	}
	if err := st.SetBookDeleted(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Fatalf("count after restore = %d, want 1", n)
	}

	off := false
	if err := st.UpdateLibrary(ctx, "legacy", nil, &off); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Fatalf("count with library disabled = %d, want 0", n)
	}
	on := true
	if err := st.UpdateLibrary(ctx, "legacy", nil, &on); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Fatalf("count with library re-enabled = %d, want 1", n)
	}

	exact, err := st.BookCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if exact != 1 {
		t.Fatalf("exact BookCount = %d, want 1", exact)
	}
}
