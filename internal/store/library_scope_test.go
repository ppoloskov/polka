package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLibraryScopeFiltersCatalogBeforePaging(t *testing.T) {
	st, _ := newLibrary(t)
	ctx := context.Background()
	addLibrary := func(name string) *Library {
		t.Helper()
		l, err := st.AddLibrary(ctx, Library{Name: name, Kind: "folder", Root: filepath.Join(t.TempDir(), name)})
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	first, second, disabled := addLibrary("First"), addLibrary("Second"), addLibrary("Disabled")
	add := func(l *Library, title, series string) int64 {
		t.Helper()
		id, err := st.AddBook(ctx, &BookInput{
			LibraryID: l.ID, Title: title,
			Authors: []AuthorName{{Last: "ScopeAuthor", First: "Shared"}},
			Series:  series, SeriesNum: 1, Genres: []string{"prose_classic"},
			Folder: "same/path", File: "same", Ext: "fb2", Rate: 5,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	firstID := add(first, "Scope Needle First", "Scope Series")
	secondID := add(second, "Scope Needle Second", "Other Series")
	disabledID := add(disabled, "Scope Needle Disabled", "Other Series")
	if err := st.UpdateLibrary(ctx, disabled.ID, nil, boolPtr(false)); err != nil {
		t.Fatal(err)
	}

	books, err := st.SearchTitles(ctx, "Scope Needle", 1, first.ID)
	if err != nil || len(books) != 1 || books[0].ID != firstID {
		t.Fatalf("scoped first search page = %+v, %v; want first book %d", books, err, firstID)
	}
	book := books[0]
	if book.LibraryID != first.ID || book.LibraryName != first.Name || !book.Available {
		t.Errorf("scoped book library metadata = %+v; want %q/%q and available", book, first.ID, first.Name)
	}
	all, err := st.SearchTitles(ctx, "Scope Needle", 10)
	if err != nil || len(all) != 2 {
		t.Fatalf("combined search = %+v, %v; want two enabled books", all, err)
	}
	seen := map[int64]bool{}
	for _, book := range all {
		seen[book.ID] = true
		if !book.Available {
			t.Errorf("combined book metadata must be available: %+v", book)
		}
	}
	if !seen[firstID] || !seen[secondID] {
		t.Errorf("combined search IDs = %v; want %d and %d", seen, firstID, secondID)
	}

	stats, err := st.SearchStats(ctx, "Scope", first.ID)
	if err != nil || stats.BookTitles != 1 || stats.Authors != 1 || stats.BookSeries != 1 {
		t.Errorf("scoped stats = %+v, %v; want one title, author, and series", stats, err)
	}
	authors, err := st.SearchAuthors(ctx, "Scope", 10, first.ID)
	if err != nil || len(authors) != 1 || authors[0].Books != 1 {
		t.Errorf("scoped author search = %+v, %v; want count 1", authors, err)
	}
	if len(authors) > 0 {
		books, _, err := st.AuthorBooks(ctx, authors[0].ID, first.ID)
		if err != nil || len(books) != 1 || books[0].ID != firstID {
			t.Errorf("scoped author books = %+v, %v; want %d", books, err, firstID)
		}
	}
	series, err := st.SearchSeries(ctx, "Scope", 10, first.ID)
	if err != nil || len(series) != 1 || series[0].Books != 1 {
		t.Errorf("scoped series search = %+v, %v; want count 1", series, err)
	}
	if len(series) > 0 {
		books, _, err := st.SeriesBooks(ctx, series[0].ID, first.ID)
		if err != nil || len(books) != 1 || books[0].ID != firstID {
			t.Errorf("scoped series books = %+v, %v; want %d", books, err, firstID)
		}
	}
	shelf, _, err := st.ShelfBooks(ctx, "genre_prose_classic", 1, 0, first.ID)
	if err != nil || len(shelf) != 1 || shelf[0].ID != firstID {
		t.Errorf("scoped genre page = %+v, %v; want first book %d", shelf, err, firstID)
	}
	if books, err := st.BooksByIDs(ctx, []int64{secondID, firstID}, first.ID); err != nil || len(books) != 1 || books[0].ID != firstID {
		t.Errorf("scoped BooksByIDs = %+v, %v; want only %d in request order", books, err, firstID)
	}
	if books, err := st.SimilarBooks(ctx, firstID, 10, first.ID); err != nil || len(books) != 0 {
		t.Errorf("scoped similar books = %+v, %v; want no cross-library candidate", books, err)
	}
	if books, err := st.SimilarBooks(ctx, firstID, 10); err != nil || len(books) != 1 || books[0].ID != secondID {
		t.Errorf("combined similar books = %+v, %v; want second book %d", books, err, secondID)
	}
	seriesNextID, err := st.AddBook(ctx, &BookInput{
		LibraryID: first.ID, Title: "Scope Series Next",
		Authors: []AuthorName{{Last: "ScopeAuthor", First: "Shared"}},
		Series:  "Scope Series", SeriesNum: 2, Folder: "same/path", File: "next", Ext: "fb2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if books, err := st.SeriesContinuations(ctx, []int64{firstID}, []int64{secondID}, 10, first.ID); err != nil || len(books) != 1 || books[0].ID != seriesNextID {
		t.Errorf("scoped series continuations = %+v, %v; want local continuation %d", books, err, seriesNextID)
	}
	if books, err := st.SeriesContinuations(ctx, []int64{firstID}, []int64{seriesNextID}, 10, first.ID); err != nil || len(books) != 0 {
		t.Errorf("scoped excluded series continuations = %+v, %v; want no books", books, err)
	}
	if books, err := st.RecommendForUser(ctx, []int64{firstID}, []int64{firstID}, 10, first.ID); err != nil || len(books) != 1 || books[0].ID != seriesNextID {
		t.Errorf("scoped recommendations = %+v, %v; want local candidate %d only", books, err, seriesNextID)
	}
	if books, err := st.RecommendForUser(ctx, []int64{firstID}, []int64{firstID}, 10); err != nil || !hasBook(books, secondID) {
		t.Errorf("combined recommendations = %+v, %v; want second book %d", books, err, secondID)
	}
	for _, scope := range []string{disabled.ID, "unknown-library"} {
		books, err := st.SearchTitles(ctx, "Scope Needle", 10, scope)
		if err != nil || len(books) != 0 {
			t.Errorf("search scope %q = %+v, %v; want no visible books", scope, books, err)
		}
	}
	if books, err := st.SearchTitles(ctx, "Scope Needle", 10, first.ID); err != nil || len(books) == 0 || books[0].ID == disabledID || books[0].ID == secondID {
		t.Errorf("first library leaked another namespace: %+v, %v", books, err)
	}
}

func hasBook(books []Book, id int64) bool {
	for _, book := range books {
		if book.ID == id {
			return true
		}
	}
	return false
}

func TestRankedSearchLibraryScopeIsAppliedBeforeLimitInLargeCatalog(t *testing.T) {
	st, _ := newLibrary(t)
	ctx := context.Background()
	foreign, err := st.AddLibrary(ctx, Library{Name: "Foreign", Kind: "folder", Root: filepath.Join(t.TempDir(), "foreign")})
	if err != nil {
		t.Fatal(err)
	}
	local, err := st.AddLibrary(ctx, Library{Name: "Local", Kind: "folder", Root: filepath.Join(t.TempDir(), "local")})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5001; i++ {
		if _, err := st.AddBook(ctx, &BookInput{
			LibraryID: foreign.ID, Title: "Rankneedle common title",
			Folder: "bulk", File: "foreign", Ext: "fb2",
		}); err != nil {
			t.Fatal(err)
		}
	}
	localID, err := st.AddBook(ctx, &BookInput{
		LibraryID: local.ID, Title: "Rankneedle common title",
		Folder: "bulk", File: "local", Ext: "fb2",
	})
	if err != nil {
		t.Fatal(err)
	}
	books, err := st.SearchTitles(ctx, "Rankneedle", 1, local.ID)
	if err != nil || len(books) != 1 || books[0].ID != localID {
		t.Fatalf("large scoped search = %+v, %v; want local book %d", books, err, localID)
	}
}

func TestPersonalBooksByIDsRetainsUnavailableOwnerMetadata(t *testing.T) {
	st, ids := newLibrary(t)
	ctx := context.Background()
	l, err := st.AddLibrary(ctx, Library{Name: "Retained", Kind: "folder", Root: filepath.Join(t.TempDir(), "retained")})
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.AddBook(ctx, &BookInput{LibraryID: l.ID, Title: "Retained title", Folder: "old", File: "entry", Ext: "txt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`UPDATE books SET missing=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if books, err := st.BooksByIDs(ctx, []int64{id}); err != nil || len(books) != 0 {
		t.Fatalf("ordinary BooksByIDs = %+v, %v; want unavailable entry hidden", books, err)
	}
	books, err := st.PersonalBooksByIDs(ctx, []int64{ids["Анна Каренина"], id, id})
	if err != nil || len(books) != 3 {
		t.Fatalf("personal BooksByIDs = %+v, %v; want requested order including duplicate", books, err)
	}
	if books[1].ID != id || books[1].Title != "Retained title" || books[1].LibraryID != l.ID || books[1].LibraryName != l.Name || books[1].Available {
		t.Errorf("retained personal book = %+v; want original identity and unavailable state", books[1])
	}
}

func boolPtr(v bool) *bool { return &v }
