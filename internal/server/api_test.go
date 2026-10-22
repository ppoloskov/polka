package server

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vestigiumincaligne/polka/internal/auth"
	"github.com/vestigiumincaligne/polka/internal/config"
	"github.com/vestigiumincaligne/polka/internal/library"
	"github.com/vestigiumincaligne/polka/internal/store"
)

const testFB2 = `<?xml version="1.0" encoding="UTF-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">
<description>
  <title-info>
    <book-title>Война и мир</book-title>
    <annotation><p>Роман-эпопея.</p></annotation>
    <coverpage><image l:href="#c.png"/></coverpage>
  </title-info>
  <publish-info><publisher>Тестиздат</publisher><year>1869</year></publish-info>
</description>
<body><section><p>Текст.</p></section></body>
<binary id="c.png" content-type="image/png">iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==</binary>
</FictionBook>`

// Same book without a cover image.
const testFB2NoCover = `<?xml version="1.0" encoding="UTF-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description>
  <title-info>
    <book-title>Без обложки</book-title>
    <annotation><p>Нет картинки.</p></annotation>
  </title-info>
</description>
<body><section><p>Текст.</p></section></body>
</FictionBook>`

// newTestServer starts a server with one book in a synthetic library.
func newTestServer(t *testing.T) (*httptest.Server, int64) {
	t.Helper()
	ts, ids := newTestLibrary(t, []testBook{{"Война и мир", testFB2}})
	return ts, ids[0]
}

type testBook struct {
	title, fb2 string
}

// newTestLibrary starts a server whose archive holds one FB2 file per book
// (100.fb2, 101.fb2, …) with a matching catalog row.
func newTestLibrary(t *testing.T, books []testBook) (*httptest.Server, []int64) {
	t.Helper()
	dir := t.TempDir()

	// Archive containing the books
	libDir := filepath.Join(dir, "lib")
	os.MkdirAll(libDir, 0o755)
	zf, err := os.Create(filepath.Join(libDir, "arch-001.zip"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	for i, b := range books {
		w, _ := zw.Create(itoa(100+i) + ".fb2")
		w.Write([]byte(b.fb2))
	}
	zw.Close()
	zf.Close()

	// Database with these books
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	ctx := context.Background()
	session, err := st.NewImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range books {
		err = session.Add(&store.BookInput{
			Title:   b.title,
			Authors: []store.AuthorName{{Last: "Толстой", First: "Лев"}},
			Genres:  []string{"prose_classic"},
			Series:  "Классика", SeriesNum: i + 1,
			Folder: "arch-001.zip", File: itoa(100 + i), Ext: "fb2",
			Size: 1000, Lang: "ru", Added: "2024-01-01", Rate: 5,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := session.Finish(); err != nil {
		t.Fatal(err)
	}

	rows, err := st.DB().Query(`SELECT id FROM books ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != len(books) {
		t.Fatalf("imported %d books, want %d", len(ids), len(books))
	}

	users, err := auth.Open(filepath.Join(dir, "users.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { users.Close() })

	cfg := &config.Config{LibraryDir: libDir, Auth: "public"}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	webFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html/>")}}
	srv := New(cfg, log, st, library.New(libDir), users, nil, webFS)

	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	return ts, ids
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("X-Polka-Lang", "ru") // tests assert Russian responses
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s -> %d", url, resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAPIEndToEnd(t *testing.T) {
	ts, bookID := newTestServer(t)

	// getConfig
	cfg := getJSON(t, ts.URL+"/main/getBooks/getConfig")
	if cfg["numberOfBooks"].(float64) != 1 {
		t.Errorf("numberOfBooks = %v", cfg["numberOfBooks"])
	}

	// getHomeShelves: a book rated 5 must land in top_rated
	shelves := getJSON(t, ts.URL+"/main/getBooks/getHomeShelves?limit=10")["shelves"].([]any)
	found := false
	for _, sh := range shelves {
		m := sh.(map[string]any)
		if m["id"] == "top_rated" && len(m["books"].([]any)) == 1 {
			book := m["books"].([]any)[0].(map[string]any)
			if book["Title"] != "Война и мир" || book["AuthorsNames"] != "Толстой Лев" {
				t.Errorf("book json = %v", book)
			}
			found = true
		}
	}
	if !found {
		t.Error("top_rated shelf is empty")
	}

	// Search
	stats := getJSON(t, ts.URL+"/main/getBooks/getSearchStats?search=войн")["searchStats"].(map[string]any)
	if stats["bookTitles"].(float64) != 1 {
		t.Errorf("searchStats = %v", stats)
	}
	titles := getJSON(t, ts.URL+"/main/getBooks/getSearchTitles?search=войн")["titlesList"].([]any)
	if len(titles) != 1 {
		t.Fatalf("titlesList = %v", titles)
	}
	authors := getJSON(t, ts.URL+"/main/getBooks/getSearchAuthors?search=толст")["authorsList"].([]any)
	if len(authors) != 1 || authors[0].(map[string]any)["Books"].(float64) != 1 {
		t.Errorf("authorsList = %v", authors)
	}
	series := getJSON(t, ts.URL+"/main/getBooks/getSearchSeries?search=класси")["seriesList"].([]any)
	if len(series) != 1 {
		t.Errorf("seriesList = %v", series)
	}

	// Author's books
	authorID := int(authors[0].(map[string]any)["AuthorID"].(float64))
	ab := getJSON(t, ts.URL+"/main/getBooks/getSearchAuthorBooks?selectedItemID="+itoa(authorID))
	if ab["title"] != "Толстой Лев" || len(ab["titlesList"].([]any)) != 1 {
		t.Errorf("author books = %v", ab)
	}

	// Book card: annotation and publication data from the FB2 in the archive
	form := getJSON(t, ts.URL+"/main/getBooks/getBookForm?selectedItemID="+itoa64(bookID))
	bf := form["bookForm"].(map[string]any)
	if bf["Genres"] != "Классическая проза" || bf["Ext"] != ".fb2" {
		t.Errorf("bookForm = %v", bf)
	}
	if !strings.Contains(form["annotation"].(string), "Роман-эпопея") {
		t.Errorf("annotation = %v", form["annotation"])
	}
	if form["publisher"] != "Тестиздат" || form["year"] != "1869" {
		t.Errorf("publish info: %v / %v", form["publisher"], form["year"])
	}

	// Cover
	resp, err := http.Get(ts.URL + "/Images/covers/" + itoa64(bookID))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("cover: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if got := resp.Header.Get("Cache-Control"); got != "private, max-age=0, must-revalidate" {
		t.Errorf("cover Cache-Control = %q", got)
	}
	if resp.Header.Get("ETag") == "" {
		t.Error("cover ETag is missing")
	}

	// fb2 download
	resp, err = http.Get(ts.URL + "/Images/fb2/" + itoa64(bookID))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "100.fb2") {
		t.Errorf("fb2 download: %d %s", resp.StatusCode, resp.Header.Get("Content-Disposition"))
	}

	// zip and compact
	for _, path := range []string{"/Images/zip/", "/Images/fb2compact/"} {
		resp, err = http.Get(ts.URL + path + itoa64(bookID))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%s -> %d", path, resp.StatusCode)
		}
	}

	// Nonexistent book
	resp, _ = http.Get(ts.URL + "/main/getBooks/getBookForm?selectedItemID=99999")
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("missing book -> %d, want 404", resp.StatusCode)
	}
}

func itoa(v int) string { return itoa64(int64(v)) }

func itoa64(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Covers carry a strong ETag (book ID + per-library cover generation) so
// conditional requests revalidate as cheap 304s, and a committed publish —
// even one without catalog changes — rotates the tag because it may have
// replaced cover content.
func TestCoverETagRevalidation(t *testing.T) {
	f := newFullServer(t, "public")
	ctx := context.Background()
	coverURL := f.ts.URL + "/Images/covers/" + itoa64(f.bookID)

	get := func(ifNoneMatch string) (int, string, []byte) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, coverURL, nil)
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("ETag"), body
	}

	status, etag, body := get("")
	if status != http.StatusOK || len(body) == 0 {
		t.Fatalf("cover -> %d, %d bytes", status, len(body))
	}
	if want := "\"" + itoa64(f.bookID) + "-0\""; etag != want {
		t.Fatalf("cover ETag = %q, want %q", etag, want)
	}

	// A matching tag (or "*") revalidates without a body.
	for _, inm := range []string{etag, "*"} {
		status, got, body := get(inm)
		if status != http.StatusNotModified || len(body) != 0 {
			t.Errorf("If-None-Match %q -> %d, %d bytes; want 304 and an empty body", inm, status, len(body))
		}
		if got != etag {
			t.Errorf("304 ETag = %q, want %q", got, etag)
		}
	}

	// An unchanged legacy publish still bumps the cover generation.
	snapshot, err := store.NewSnapshot(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.PublishLibrary(ctx, "legacy", snapshot, true); err != nil {
		snapshot.Close()
		t.Fatal(err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}

	status, rotated, body := get(etag)
	if status != http.StatusOK || len(body) == 0 {
		t.Errorf("stale If-None-Match after publish -> %d, %d bytes; want 200 with a body", status, len(body))
	}
	if want := "\"" + itoa64(f.bookID) + "-1\""; rotated != want {
		t.Errorf("cover ETag after publish = %q, want %q", rotated, want)
	}
}

// A missing cover answers 404 with a cache allowance, so browsers stop
// re-requesting it on every page view; successful covers are unchanged.
func TestCoverMissingIsCacheable(t *testing.T) {
	ts, ids := newTestLibrary(t, []testBook{
		{"Война и мир", testFB2},
		{"Без обложки", testFB2NoCover},
	})

	resp, err := http.Get(ts.URL + "/Images/covers/" + itoa64(ids[1]))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("coverless book -> %d, want 404", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "private, max-age=3600" {
		t.Errorf("coverless Cache-Control = %q, want %q", got, "private, max-age=3600")
	}

	resp, err = http.Get(ts.URL + "/Images/covers/" + itoa64(ids[0]))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("cover -> %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "private, max-age=0, must-revalidate" {
		t.Errorf("cover Cache-Control = %q", got)
	}
	if resp.Header.Get("ETag") == "" {
		t.Error("cover ETag is missing")
	}
}
