package server

import (
	"fmt"
	"testing"

	"github.com/vestigiumincaligne/polka/internal/store"
)

// The author endpoint reports hasMore by fetching one extra row beyond the
// requested page — a three-book author requested with limit=2 has a second
// page; with limit=3 it does not.
func TestAuthorPageHasMore(t *testing.T) {
	f := newFullServer(t, "required")
	ctx := t.Context()
	for i := 1; i <= 3; i++ {
		if _, err := f.st.AddBook(ctx, &store.BookInput{
			Title:   fmt.Sprintf("Author volume %d", i),
			Authors: []store.AuthorName{{Last: "Полиглот", First: "Многотомный"}},
			Genres:  []string{"prose_classic"}, Folder: "f", File: fmt.Sprintf("v%d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	var authorID int64
	if err := f.st.DB().QueryRowContext(ctx, `SELECT id FROM authors WHERE last_name = 'Полиглот'`).Scan(&authorID); err != nil {
		t.Fatal(err)
	}

	page := func(limit int) map[string]any {
		t.Helper()
		url := fmt.Sprintf("%s/main/getBooks/getSearchAuthorBooks?selectedItemID=%d&limit=%d", f.ts.URL, authorID, limit)
		return getJSONWith(t, f.reader, url)
	}

	first := page(2)
	if got := len(first["titlesList"].([]any)); got != 2 {
		t.Fatalf("author page = %d books; want the requested 2", got)
	}
	if !first["hasMore"].(bool) {
		t.Fatal("author page hasMore = false; want true (a third book remains)")
	}
	third := page(3)
	if got := len(third["titlesList"].([]any)); got != 3 {
		t.Fatalf("author page = %d books; want all three", got)
	}
	if third["hasMore"].(bool) {
		t.Fatal("author page hasMore = true; want false on the last page")
	}
}
