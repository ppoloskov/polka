package server

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/vestigiumincaligne/polka/internal/auth"
	"github.com/vestigiumincaligne/polka/internal/collections"
	"github.com/vestigiumincaligne/polka/internal/store"
)

func TestCatalogEndpointsAcceptLibraryScope(t *testing.T) {
	f := newFullServer(t, "required")
	ctx := t.Context()
	first, err := f.st.AddLibrary(ctx, store.Library{Name: "First scope", Kind: "folder", Root: filepath.Join(t.TempDir(), "first")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.st.AddLibrary(ctx, store.Library{Name: "Second scope", Kind: "folder", Root: filepath.Join(t.TempDir(), "second")})
	if err != nil {
		t.Fatal(err)
	}
	add := func(libraryID, title string) int64 {
		t.Helper()
		id, err := f.st.AddBook(ctx, &store.BookInput{
			LibraryID: libraryID, Title: title, Folder: "same/path", File: "same", Ext: "fb2",
			Genres: []string{"prose_classic"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	firstID := add(first.ID, "Library scope needle first")
	add(second.ID, "Library scope needle second")
	query := f.ts.URL + "/main/getBooks/getSearchTitles?search=Library+scope+needle&libraryId=" + first.ID
	page := getJSONWith(t, f.reader, query)["titlesList"].([]any)
	if len(page) != 1 {
		t.Fatalf("scoped title search returned %d books: %v", len(page), page)
	}
	book := page[0].(map[string]any)
	if book["BookID"] != float64(firstID) || book["LibraryID"] != first.ID || book["LibraryName"] != first.Name || book["Available"] != true {
		t.Errorf("scoped title card = %v", book)
	}
	stats := getJSONWith(t, f.reader, f.ts.URL+"/main/getBooks/getSearchStats?search=Library+scope+needle&libraryId="+first.ID)["searchStats"].(map[string]any)
	if stats["bookTitles"] != float64(1) {
		t.Errorf("scoped search stats = %v; want one title", stats)
	}
	shelf := getJSONWith(t, f.reader, f.ts.URL+"/main/getBooks/getShelfBooks?shelfId=genre_prose_classic&limit=1&libraryId="+first.ID)["titlesList"].([]any)
	if len(shelf) != 1 || shelf[0].(map[string]any)["BookID"] != float64(firstID) {
		t.Errorf("scoped genre shelf = %v", shelf)
	}
	unknown := getJSONWith(t, f.reader, f.ts.URL+"/main/getBooks/getSearchTitles?search=Library+scope+needle&libraryId=unknown")
	if len(unknown["titlesList"].([]any)) != 0 {
		t.Errorf("unknown library returned books: %v", unknown)
	}
	collectionBooks := []struct {
		libraryID string
		title     string
	}{
		{first.ID, "Collection scope first"},
		{second.ID, "Collection scope second"},
	}
	items := make([]collections.FileItem, 0, len(collectionBooks))
	var collectionSecondID int64
	for _, item := range collectionBooks {
		id, err := f.st.AddBook(ctx, &store.BookInput{
			LibraryID: item.libraryID, Title: item.title,
			Authors: []store.AuthorName{{Last: "ScopeWriter", First: "Test"}},
			Folder:  "same", File: item.title, Ext: "fb2",
		})
		if err != nil {
			t.Fatal(err)
		}
		if item.libraryID == second.ID {
			collectionSecondID = id
		}
		items = append(items, collections.FileItem{Title: item.title, Author: "Test ScopeWriter"})
	}
	cols, err := collections.Open(filepath.Join(f.dir, "collections.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cols.Close() })
	if _, err := cols.Import(ctx, &collections.File{Slug: "library-scope", Title: "Scoped entries", Items: items}); err != nil {
		t.Fatal(err)
	}
	if _, err := cols.Match(ctx, f.st, "library-scope"); err != nil {
		t.Fatal(err)
	}
	collectionPage := getJSONWith(t, f.reader, f.ts.URL+"/main/getBooks/getShelfBooks?shelfId=collection_library-scope&limit=1&libraryId="+second.ID)
	collectionCards := collectionPage["titlesList"].([]any)
	if len(collectionCards) != 1 || collectionCards[0].(map[string]any)["BookID"] != float64(collectionSecondID) || collectionPage["hasMore"] != false {
		t.Errorf("scoped collection page = %v; want second-library item %d even though it follows another library", collectionPage, collectionSecondID)
	}
}

func TestPersonalShelvesRetainMissingBookMetadata(t *testing.T) {
	f := newFullServer(t, "required")
	ctx := t.Context()
	l, err := f.st.AddLibrary(ctx, store.Library{Name: "Retained root", Kind: "folder", Root: filepath.Join(t.TempDir(), "retained")})
	if err != nil {
		t.Fatal(err)
	}
	bookID, err := f.st.AddBook(ctx, &store.BookInput{LibraryID: l.ID, Title: "Remembered title", Folder: "lost", File: "entry", Ext: "txt"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.st.AddLibrary(ctx, store.Library{Name: "Other root", Kind: "folder", Root: filepath.Join(t.TempDir(), "other")})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := f.users.GetByLogin(ctx, "reader")
	if err != nil {
		t.Fatal(err)
	}
	wishlist, err := f.users.Wishlist(ctx, reader.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.users.AddToList(ctx, reader.ID, wishlist.ID, bookID); err != nil {
		t.Fatal(err)
	}
	if err := f.users.SaveProgress(ctx, reader.ID, bookID, auth.Progress{Chapter: 0, Position: 0.25, Overall: 0.25}); err != nil {
		t.Fatal(err)
	}
	// More recent foreign-library references must not consume a scoped shelf's
	// page before the retained book from the selected library.
	for i := 0; i < 33; i++ {
		foreignID, err := f.st.AddBook(ctx, &store.BookInput{LibraryID: other.ID, Title: fmt.Sprintf("Other remembered %02d", i), Folder: "other", File: fmt.Sprint(i), Ext: "txt"})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.users.AddToList(ctx, reader.ID, wishlist.ID, foreignID); err != nil {
			t.Fatal(err)
		}
		if err := f.users.SaveProgress(ctx, reader.ID, foreignID, auth.Progress{Chapter: 0, Position: 0.25, Overall: 0.25}); err != nil {
			t.Fatal(err)
		}
	}
	created := postJSON(t, f.reader, f.ts.URL+"/api/v1/lists", map[string]any{"name": "Saved"})
	var listResp map[string]map[string]any
	decode(t, created, &listResp)
	listID := itoa64(int64(listResp["list"]["id"].(float64)))
	postJSON(t, f.reader, f.ts.URL+"/api/v1/lists/"+listID+"/books", map[string]any{"bookId": bookID}).Body.Close()
	if _, err := f.st.DB().Exec(`UPDATE books SET missing=1 WHERE id=?`, bookID); err != nil {
		t.Fatal(err)
	}

	assertRetained := func(label string, cards []any) {
		t.Helper()
		for _, card := range cards {
			book, ok := card.(map[string]any)
			if ok && book["BookID"] == float64(bookID) {
				if book["Title"] != "Remembered title" || book["LibraryID"] != l.ID || book["LibraryName"] != l.Name || book["Available"] != false {
					t.Errorf("%s retained card = %v", label, book)
				}
				return
			}
		}
		t.Errorf("%s omitted missing personal book %d: %v", label, bookID, cards)
	}
	home := getJSONWith(t, f.reader, f.ts.URL+"/main/getBooks/getHomeShelves?limit=50")["shelves"].([]any)
	for _, shelf := range home {
		item := shelf.(map[string]any)
		if item["id"] == "wishlist" || item["id"] == "reading" {
			assertRetained(item["id"].(string), item["books"].([]any))
		}
	}
	scoped := getJSONWith(t, f.reader, f.ts.URL+"/main/getBooks/getHomeShelves?limit=1&libraryId="+l.ID)["shelves"].([]any)
	for _, shelf := range scoped {
		item := shelf.(map[string]any)
		if item["id"] != "wishlist" && item["id"] != "reading" {
			continue
		}
		found := false
		for _, raw := range item["books"].([]any) {
			card := raw.(map[string]any)
			if card["LibraryID"] != l.ID {
				t.Errorf("scoped %s shelf leaked another library: %v", item["id"], card)
			}
			if card["BookID"] == float64(bookID) && card["Available"] == false {
				found = true
			}
		}
		if !found {
			t.Errorf("scoped %s shelf omitted retained book %d: %v", item["id"], bookID, item["books"])
		}
	}
	list := getJSONWith(t, f.reader, f.ts.URL+"/api/v1/lists/"+listID+"/books")
	assertRetained("personal list", list["titlesList"].([]any))
}
