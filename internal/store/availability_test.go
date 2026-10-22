package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingAndDisabledBooksAreUnavailable(t *testing.T) {
	st, ids := newLibrary(t)
	ctx := context.Background()

	disabled, err := st.AddLibrary(ctx, Library{
		Name: "Disabled", Kind: "folder", Root: filepath.Join(t.TempDir(), "disabled"), Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	disabledID, err := st.AddBook(ctx, &BookInput{
		LibraryID: disabled.ID, Title: "Hidden Garden",
		Authors: []AuthorName{{Last: "Толстой", First: "Лев"}},
		Series:  "Классика", SeriesNum: 3, Genres: []string{"prose_classic"},
		Folder: "same", File: "hidden", Ext: "fb2", Rate: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	visibleID, err := st.AddBook(ctx, &BookInput{
		LibraryID: "legacy", Title: "Hidden Garden Walk", Folder: "one.zip", File: "visible", Ext: "fb2", Rate: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`UPDATE libraries SET enabled = 0 WHERE id = ?`, disabled.ID); err != nil {
		t.Fatal(err)
	}
	missingID := ids["Анна Каренина"]
	if _, err := st.DB().Exec(`UPDATE books SET missing = 1 WHERE id = ?`, missingID); err != nil {
		t.Fatal(err)
	}
	legacyID := ids["Война и мир"]

	if got, err := st.BookCount(ctx); err != nil || got != 6 {
		t.Errorf("BookCount = %d, %v; want 6 visible books", got, err)
	}
	for _, query := range []string{"Каренина"} {
		books, err := st.SearchTitles(ctx, query, 10)
		if err != nil || len(books) != 0 {
			t.Errorf("SearchTitles(%q) = %v, %v; want no unavailable books", query, titles(books), err)
		}
	}
	page, err := st.SearchTitles(ctx, "Hidden Garden", 1)
	if err != nil || len(page) != 1 || page[0].ID != visibleID {
		t.Errorf("first search page = %v, %v; want visible match %d", titles(page), err, visibleID)
	}
	hiddenPage, err := st.SearchTitles(ctx, "Hidden", 10)
	if err != nil || len(hiddenPage) != 1 || hiddenPage[0].ID != visibleID {
		t.Errorf("visible Hidden title search = %v, %v; want only %d", titles(hiddenPage), err, visibleID)
	}
	stats, err := st.SearchStats(ctx, "Каренина")
	if err != nil || stats.BookTitles != 0 {
		t.Errorf("SearchStats(Каренина) = %+v, %v; want no visible title match", stats, err)
	}

	shelf, _, err := st.ShelfBooks(ctx, "top_rated", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if containsTitle(shelf, "Анна Каренина") || containsTitle(shelf, "Hidden Garden") || !containsTitle(shelf, "Война и мир") {
		t.Errorf("top-rated shelf = %v; should omit unavailable books and retain legacy books", titles(shelf))
	}

	authors, err := st.AuthorsByPrefix(ctx, "Тол", "Том", 10, 0)
	if err != nil || len(authors) != 1 || authors[0].Books != 1 {
		t.Errorf("Толстой author count = %+v, %v; want one visible book", authors, err)
	}
	searchAuthors, err := st.SearchAuthors(ctx, "Толстой", 10)
	if err != nil || len(searchAuthors) != 1 || searchAuthors[0].Books != 1 {
		t.Errorf("SearchAuthors count = %+v, %v; want one visible book", searchAuthors, err)
	}
	series, err := st.SeriesByPrefix(ctx, "Кла", "Клб", 10, 0)
	if err != nil || len(series) != 1 || series[0].Books != 1 {
		t.Errorf("Классика series count = %+v, %v; want one visible book", series, err)
	}
	searchSeries, err := st.SearchSeries(ctx, "Классика", 10)
	if err != nil || len(searchSeries) != 1 || searchSeries[0].Books != 1 {
		t.Errorf("SearchSeries count = %+v, %v; want one visible book", searchSeries, err)
	}

	if len(authors) > 0 {
		books, _, err := st.AuthorBooks(ctx, authors[0].ID)
		if err != nil || len(books) != 1 || books[0].ID != legacyID {
			t.Errorf("author books = %v, %v; want visible legacy book %d", titles(books), err, legacyID)
		}
	}
	if len(series) > 0 {
		books, _, err := st.SeriesBooks(ctx, series[0].ID)
		if err != nil || len(books) != 1 || books[0].ID != legacyID {
			t.Errorf("series books = %v, %v; want visible legacy book %d", titles(books), err, legacyID)
		}
	}

	recs, err := st.RecommendForUser(ctx, []int64{legacyID}, []int64{legacyID}, 10)
	if err != nil || len(recs) != 0 {
		t.Errorf("recommendations = %v, %v; want no unavailable books", titles(recs), err)
	}
	continuations, err := st.SeriesContinuations(ctx, []int64{legacyID}, nil, 10)
	if err != nil || len(continuations) != 0 {
		t.Errorf("series continuations = %v, %v; want no missing next book", titles(continuations), err)
	}

	for _, id := range []int64{missingID, disabledID} {
		if _, err := st.BookFile(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("BookFile(%d) err = %v; want ErrNotFound", id, err)
		}
		if _, err := st.BookDetails(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("BookDetails(%d) err = %v; want ErrNotFound", id, err)
		}
		if _, err := st.LibraryForBook(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("LibraryForBook(%d) err = %v; want ErrNotFound", id, err)
		}
	}
	files, err := st.BookFilesByIDs(ctx, []int64{legacyID, missingID, disabledID})
	if err != nil || len(files) != 1 || files[0].ID != legacyID {
		t.Errorf("BookFilesByIDs = %+v, %v; want only legacy book %d", files, err, legacyID)
	}
	owner, err := st.LibraryForBook(ctx, legacyID)
	if err != nil || owner == nil || owner.ID != "legacy" {
		t.Errorf("legacy LibraryForBook = %+v, %v", owner, err)
	}

	if err := st.SetBookDeleted(ctx, missingID, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetBookDeleted(ctx, missingID, false); err != nil {
		t.Fatal(err)
	}
	if got, err := st.BookCount(ctx); err != nil || got != 6 {
		t.Errorf("BookCount after restoring missing book = %d, %v; want 6", got, err)
	}
	afterRestore, err := st.SearchTitles(ctx, "Каренина", 10)
	if err != nil || len(afterRestore) != 0 {
		t.Errorf("restored missing title search = %v, %v; want no visible book", titles(afterRestore), err)
	}
	var indexed int
	if err := st.DB().QueryRow(`SELECT count(*) FROM book_search WHERE rowid = ?`, missingID).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if indexed != 0 {
		t.Errorf("restored missing book has %d full-text rows; want it to remain unindexed", indexed)
	}
}

func containsTitle(books []Book, title string) bool {
	for _, book := range books {
		if strings.EqualFold(book.Title, title) {
			return true
		}
	}
	return false
}
