package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vestigiumincaligne/polka/internal/collections"
)

type managedBook struct {
	id      int64
	folder  string
	file    string
	ext     string
	missing int
}

func libraryJSON(t *testing.T, client *http.Client, url string) map[string]any {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("GET %s = %d %q, want JSON API response", url, resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode GET %s JSON: %v", url, err)
	}
	return result
}

func managedLibraryID(t *testing.T, f *fullServer, name string) string {
	t.Helper()
	data := libraryJSON(t, f.admin, f.ts.URL+"/admin/libraries")
	libraries, ok := data["libraries"].([]any)
	if !ok {
		t.Fatalf("admin library list has no libraries array: %v", data)
	}
	for _, item := range libraries {
		library, _ := item.(map[string]any)
		if library["name"] == name {
			id, _ := library["id"].(string)
			return id
		}
	}
	t.Fatalf("library %q missing from admin list: %v", name, data)
	return ""
}

func createFolderLibrary(t *testing.T, f *fullServer, name, root string) string {
	t.Helper()
	resp := postJSON(t, f.admin, f.ts.URL+"/admin/libraries", map[string]any{
		"name": name, "kind": "folder", "root": root,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create library %q: %d %s", name, resp.StatusCode, body)
	}
	return managedLibraryID(t, f, name)
}

func startLibraryUpdate(t *testing.T, f *fullServer, id string, allowEmpty ...bool) {
	t.Helper()
	request := map[string]any{}
	if len(allowEmpty) > 0 {
		request["allowEmpty"] = allowEmpty[0]
	}
	resp := postJSON(t, f.admin, f.ts.URL+"/admin/libraries/"+id+"/update", request)
	defer resp.Body.Close()
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode != http.StatusOK || result["started"] != true {
		t.Fatalf("start update %q: %d %v", id, resp.StatusCode, result)
	}
}

func waitLibraryUpdate(t *testing.T, f *fullServer, id string, expectError ...bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := f.admin.Get(f.ts.URL + "/admin/import/status")
		if err != nil {
			t.Fatal(err)
		}
		var status map[string]any
		err = json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("decode update status: %v", err)
		}
		if status["running"] == false {
			if status["libraryId"] != id {
				t.Fatalf("status libraryId = %v, want %s", status["libraryId"], id)
			}
			for _, field := range []string{"added", "removed", "changed", "unchanged"} {
				if _, ok := status[field].(float64); !ok {
					t.Fatalf("status omitted %s count: %v", field, status)
				}
			}
			phase, wantError := "done", false
			if len(expectError) > 0 && expectError[0] {
				phase, wantError = "error", true
			}
			if status["phase"] != phase || (status["error"] != "") != wantError {
				t.Fatalf("library update failed: %v", status)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("library update did not finish within 5 seconds")
}

func libraryGenreCount(t *testing.T, f *fullServer, code string) int {
	t.Helper()
	resp, err := f.reader.Get(f.ts.URL + "/main/getBooks/getSearchGenres?search=" + code)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result struct {
		Genres []struct {
			Code  string `json:"GenreCode"`
			Books int    `json:"Books"`
		} `json:"genresList"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	for _, genre := range result.Genres {
		if genre.Code == code {
			return genre.Books
		}
	}
	return 0
}

func libraryInpxFixture(t *testing.T, title, genre string) []byte {
	t.Helper()
	var data bytes.Buffer
	zw := zip.NewWriter(&data)
	for name, contents := range map[string]string{
		"collection.info": "Library update fixture\n",
		"books.inp":       strings.Join([]string{"Writer,Example,", genre, title, "Series", "1", "source-locator", "100", "1", "0", "fb2", "2024-01-02", "en", "4", "", "2020", "source-id"}, "\x04") + "\r\n",
	} {
		entry, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestLegacyLibraryUpdateRequiresSavedSourcePath(t *testing.T) {
	ts, admin, _, _ := newManageServerDir(t)
	resp := postJSON(t, admin, ts.URL+"/admin/libraries/legacy/update", map[string]any{})
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(strings.ToLower(string(body)), "source path") {
		t.Fatalf("legacy update without a saved source = %d %q, want helpful 400", resp.StatusCode, body)
	}
}

func TestLegacyLibraryUpdateUsesSavedInpxAndReportsDiff(t *testing.T) {
	ts, admin, st, dir := newManageServerDir(t)
	status, result := postImportForm(t, admin, ts.URL+"/admin/import/inpx", map[string]string{"replace": "1"}, "saved.inpx", webImportFixture(t, "Saved source book"))
	if status != http.StatusOK || result["started"] != true {
		t.Fatalf("initial legacy import = %d %v", status, result)
	}
	importStatus := waitForImportStatus(t, admin, ts.URL+"/admin/import/status")
	if importStatus["phase"] != "done" || importStatus["libraryId"] != "legacy" ||
		importStatus["added"].(float64) != 1 || importStatus["removed"].(float64) != 0 ||
		importStatus["changed"].(float64) != 0 || importStatus["unchanged"].(float64) != 0 {
		t.Fatalf("initial legacy import status = %v", importStatus)
	}
	var originalID int64
	if err := st.DB().QueryRow(`SELECT id FROM books WHERE library_id='legacy' AND deleted=0`).Scan(&originalID); err != nil {
		t.Fatal(err)
	}
	legacy, err := st.Library(context.Background(), "legacy")
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := legacy.SourcePath
	cs, err := collections.Open(filepath.Join(dir, "collections.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	collectionFile, err := collections.Parse(strings.NewReader(`{"slug":"saved-source","title":"Saved source","items":[{"title":"Saved source book","author":"Example Writer"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Import(context.Background(), collectionFile); err != nil {
		t.Fatal(err)
	}
	collection, err := cs.Get(context.Background(), "saved-source")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Match(context.Background(), st, collection.Slug); err != nil {
		t.Fatal(err)
	}
	matched, err := cs.BookIDs(context.Background(), collection.ID, 10, 0)
	if err != nil || len(matched) != 1 || matched[0] != originalID {
		t.Fatalf("initial collection book IDs = %v, err %v; want %d", matched, err, originalID)
	}
	changedSource := webImportFixture(t, "Changed saved source book")
	if err := os.WriteFile(sourcePath, changedSource, 0600); err != nil {
		t.Fatal(err)
	}

	resp := postJSON(t, admin, ts.URL+"/admin/libraries/legacy/update", map[string]any{})
	defer resp.Body.Close()
	var started map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&started)
	if resp.StatusCode != http.StatusOK || started["started"] != true {
		t.Fatalf("saved legacy update = %d %v", resp.StatusCode, started)
	}
	updated := waitForImportStatus(t, admin, ts.URL+"/admin/import/status")
	deadline := time.Now().Add(5 * time.Second)
	for updated["warning"] == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		updated = libraryJSON(t, admin, ts.URL+"/admin/import/status")
	}
	if updated["phase"] != "done" || updated["libraryId"] != "legacy" ||
		updated["added"].(float64) != 1 || updated["removed"].(float64) != 1 ||
		updated["changed"].(float64) != 0 || updated["unchanged"].(float64) != 0 {
		t.Fatalf("saved legacy update did not report expected diff: %v", updated)
	}
	if warning, _ := updated["warning"].(string); warning == "" {
		t.Fatalf("saved legacy update did not rerun the missing-archive warning hook: %v", updated)
	}
	var currentID int64
	if err := st.DB().QueryRow(`SELECT id FROM books WHERE library_id='legacy' AND deleted=0 AND missing=0`).Scan(&currentID); err != nil || currentID == originalID {
		t.Fatalf("legacy identity after saved-source update = %d (err %v), want a new ID after changed metadata", currentID, err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for matched, err = cs.BookIDs(context.Background(), collection.ID, 10, 0); err == nil && len(matched) != 0 && time.Now().Before(deadline); matched, err = cs.BookIDs(context.Background(), collection.ID, 10, 0) {
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || len(matched) != 0 {
		t.Fatalf("collection IDs after changed title = %v, err %v; want unmatched old title", matched, err)
	}
}

func TestSavedInpxLibraryUpdateIsScopedAndFailureSafe(t *testing.T) {
	f := newFullServer(t, "required")
	rootOne, rootTwo := t.TempDir(), t.TempDir()
	sourceOne := filepath.Join(t.TempDir(), "one.inpx")
	sourceTwo := filepath.Join(t.TempDir(), "two.inpx")
	initial := libraryInpxFixture(t, "Saved source book", "fiction")
	for _, path := range []string{sourceOne, sourceTwo} {
		if err := os.WriteFile(path, initial, 0600); err != nil {
			t.Fatal(err)
		}
	}
	addInpx := func(name, root, source string) string {
		t.Helper()
		resp := postJSON(t, f.admin, f.ts.URL+"/admin/libraries", map[string]any{
			"name": name, "kind": "inpx", "root": root, "sourcePath": source,
		})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("create INPX library %q: %d %s", name, resp.StatusCode, body)
		}
		return managedLibraryID(t, f, name)
	}
	one := addInpx("Catalog one", rootOne, sourceOne)
	two := addInpx("Catalog two", rootTwo, sourceTwo)
	startLibraryUpdate(t, f, one)
	waitLibraryUpdate(t, f, one)
	startLibraryUpdate(t, f, two)
	waitLibraryUpdate(t, f, two)
	firstOne := managedBooks(t, f, one)["books.zip/source-locator.fb2"]
	firstTwo := managedBooks(t, f, two)["books.zip/source-locator.fb2"]
	if firstOne.id == 0 || firstTwo.id == 0 || firstOne.id == firstTwo.id {
		t.Fatalf("same INPX source in two libraries IDs = %d and %d; rows %v / %v", firstOne.id, firstTwo.id, managedBooks(t, f, one), managedBooks(t, f, two))
	}
	if got := libraryGenreCount(t, f, "fiction"); got != 2 {
		t.Fatalf("fiction count after first updates = %d, want 2", got)
	}

	startLibraryUpdate(t, f, one)
	waitLibraryUpdate(t, f, one)
	status := libraryJSON(t, f.admin, f.ts.URL+"/admin/import/status")
	if status["unchanged"].(float64) != 1 || status["libraryId"] != one {
		t.Fatalf("repeat INPX update status = %v", status)
	}
	var oldHash string
	if err := f.st.DB().QueryRow(`SELECT value FROM meta WHERE key=?`, "library."+one+".inpx_sha256").Scan(&oldHash); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceOne, []byte("not a valid INPX archive"), 0600); err != nil {
		t.Fatal(err)
	}
	startLibraryUpdate(t, f, one)
	waitLibraryUpdate(t, f, one, true)
	if got := managedBooks(t, f, one)["books.zip/source-locator.fb2"]; got.id != firstOne.id || got.missing != 0 {
		t.Fatalf("row after corrupt update = %+v, want original present row", got)
	}
	var hashAfterFailure string
	if err := f.st.DB().QueryRow(`SELECT value FROM meta WHERE key=?`, "library."+one+".inpx_sha256").Scan(&hashAfterFailure); err != nil || hashAfterFailure != oldHash {
		t.Fatalf("source hash after failed update = %q, err %v; want %q", hashAfterFailure, err, oldHash)
	}

	changed := libraryInpxFixture(t, "Changed source book", "drama")
	if err := os.WriteFile(sourceOne, changed, 0600); err != nil {
		t.Fatal(err)
	}
	startLibraryUpdate(t, f, one)
	waitLibraryUpdate(t, f, one)
	updated := managedBooks(t, f, one)
	if got := updated["books.zip/source-locator.fb2"]; got.id == 0 || got.id == firstOne.id || got.missing != 0 {
		t.Fatalf("changed source metadata did not receive a fresh live ID: %+v", got)
	}
	var oldMissing int
	if err := f.st.DB().QueryRow(`SELECT missing FROM books WHERE id=?`, firstOne.id).Scan(&oldMissing); err != nil || oldMissing != 1 {
		t.Fatalf("old row missing state = %d, err %v; want retained missing row", oldMissing, err)
	}
	if got := managedBooks(t, f, two)["books.zip/source-locator.fb2"]; got.id != firstTwo.id || got.missing != 0 {
		t.Fatalf("other library changed during update: %+v, want ID %d present", got, firstTwo.id)
	}
	if fiction, drama := libraryGenreCount(t, f, "fiction"), libraryGenreCount(t, f, "drama"); fiction != 1 || drama != 1 {
		t.Fatalf("genre counts after saved-source update = fiction %d, drama %d; want 1 each", fiction, drama)
	}
	resp := postJSON(t, f.admin, f.ts.URL+"/admin/libraries/"+two, map[string]any{"enabled": false})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disable second INPX library: %d", resp.StatusCode)
	}
	if got := libraryGenreCount(t, f, "fiction"); got != 0 {
		t.Fatalf("fiction count after disabling its only library = %d, want 0", got)
	}
}

func managedBooks(t *testing.T, f *fullServer, libraryID string) map[string]managedBook {
	t.Helper()
	rows, err := f.st.DB().Query(`
		SELECT b.id,f.name,b.file,b.ext,b.missing
		FROM books b JOIN folders f ON f.id=b.folder_id
		WHERE b.library_id=? AND b.source_key!=''`, libraryID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	books := make(map[string]managedBook)
	for rows.Next() {
		var book managedBook
		if err := rows.Scan(&book.id, &book.folder, &book.file, &book.ext, &book.missing); err != nil {
			t.Fatal(err)
		}
		book.file = filepath.ToSlash(filepath.Join(book.folder, book.file+"."+book.ext))
		if current, ok := books[book.file]; !ok || (current.missing != 0 && book.missing == 0) {
			books[book.file] = book
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return books
}

func writeManagedText(t *testing.T, root, name, value string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestAdminLibraryUpdateLifecycleAndRootIsolation(t *testing.T) {
	f := newFullServer(t, "required")
	rootOne, rootTwo := t.TempDir(), t.TempDir()
	writeManagedText(t, rootOne, "same.txt", "root one contents")
	writeManagedText(t, rootTwo, "same.txt", "root two contents")

	one := createFolderLibrary(t, f, "One", rootOne)
	var before int
	if err := f.st.DB().QueryRow(`SELECT count(*) FROM books WHERE library_id=?`, one).Scan(&before); err != nil || before != 0 {
		t.Fatalf("adding a source automatically imported %d rows, err %v", before, err)
	}
	startLibraryUpdate(t, f, one)
	waitLibraryUpdate(t, f, one)
	booksOne := managedBooks(t, f, one)
	first := booksOne["same.txt"]
	if first.id == 0 || first.missing != 0 {
		t.Fatalf("first source row = %+v", first)
	}
	reader, err := f.users.GetByLogin(context.Background(), "reader")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.users.RateBook(context.Background(), reader.ID, first.id, 5); err != nil {
		t.Fatal(err)
	}
	startLibraryUpdate(t, f, one)
	waitLibraryUpdate(t, f, one)
	if repeated := managedBooks(t, f, one)["same.txt"]; repeated.id != first.id {
		t.Fatalf("unchanged source ID = %d, want %d", repeated.id, first.id)
	}
	resp := postJSON(t, f.admin, f.ts.URL+"/admin/libraries/"+one, map[string]any{"root": rootTwo})
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		resp.Body.Close()
		t.Fatalf("attempt to change saved root returned %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp, err = f.reader.Get(f.ts.URL + "/Images/fb2/" + fmt.Sprint(first.id))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || string(body) != "root one contents" {
		t.Fatalf("saved root changed through update API: %d %q, %v", resp.StatusCode, body, err)
	}

	// Same relative filename in another root is a separate book and serves from
	// that library's root.
	two := createFolderLibrary(t, f, "Two", rootTwo)
	startLibraryUpdate(t, f, two)
	waitLibraryUpdate(t, f, two)
	second := managedBooks(t, f, two)["same.txt"]
	if second.id == 0 || second.id == first.id {
		t.Fatalf("same filename in another library IDs = %d and %d", first.id, second.id)
	}
	if err := f.users.RateBook(context.Background(), reader.ID, second.id, 4); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]string{first.id: "root one contents", second.id: "root two contents"} {
		resp, err := f.reader.Get(f.ts.URL + "/Images/fb2/" + fmt.Sprint(id))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || string(body) != want {
			t.Fatalf("book %d download = %d %q, err %v; want %q", id, resp.StatusCode, body, err, want)
		}
	}

	writeManagedText(t, rootOne, "same.txt", "changed contents")
	startLibraryUpdate(t, f, one)
	waitLibraryUpdate(t, f, one)
	changed := managedBooks(t, f, one)["same.txt"]
	if changed.id == first.id {
		t.Fatalf("changed file reused prior identity: old %+v, new %+v", first, changed)
	}
	var oldMissing int
	if err := f.st.DB().QueryRow(`SELECT missing FROM books WHERE id=?`, first.id).Scan(&oldMissing); err != nil || oldMissing != 1 {
		t.Fatalf("old row missing state = %d, %v; want 1", oldMissing, err)
	}
	if rating := f.users.UserRating(context.Background(), reader.ID, first.id); rating != 5 {
		t.Fatalf("old personal rating after source change = %d, want retained 5", rating)
	}
	if err := os.Remove(filepath.Join(rootOne, "same.txt")); err != nil {
		t.Fatal(err)
	}
	startLibraryUpdate(t, f, one)
	waitLibraryUpdate(t, f, one, true)
	var missingAfterRejectedEmpty int
	if err := f.st.DB().QueryRow(`SELECT missing FROM books WHERE id=?`, changed.id).Scan(&missingAfterRejectedEmpty); err != nil || missingAfterRejectedEmpty != 0 {
		t.Fatalf("row after unapproved empty update = missing %d, %v; want present", missingAfterRejectedEmpty, err)
	}
	startLibraryUpdate(t, f, one, true)
	waitLibraryUpdate(t, f, one)
	var changedMissing int
	if err := f.st.DB().QueryRow(`SELECT missing FROM books WHERE id=?`, changed.id).Scan(&changedMissing); err != nil || changedMissing != 1 {
		t.Fatalf("removed source row missing state = %d, %v; want 1", changedMissing, err)
	}

	resp = postJSON(t, f.admin, f.ts.URL+"/admin/libraries/"+two, map[string]any{"enabled": false, "name": "Second"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disable library: %d", resp.StatusCode)
	}
	public := libraryJSON(t, f.reader, f.ts.URL+"/api/v1/libraries")
	encoded, _ := json.Marshal(public)
	if strings.Contains(string(encoded), rootOne) || strings.Contains(string(encoded), rootTwo) || strings.Contains(string(encoded), "sourcePath") {
		t.Fatalf("public library list exposes filesystem paths: %s", encoded)
	}
	publicHasOne := false
	for _, item := range public["libraries"].([]any) {
		fields := item.(map[string]any)
		if len(fields) != 3 || fields["id"] == nil || fields["name"] == nil || fields["kind"] == nil {
			t.Fatalf("public library fields = %v, want only id/name/kind", fields)
		}
		if fields["id"] == two {
			t.Fatalf("disabled library %q appears in public list", two)
		}
		publicHasOne = publicHasOne || fields["id"] == one
	}
	if !publicHasOne {
		t.Fatalf("enabled library %q is missing from public list: %v", one, public)
	}
	resp, err = f.reader.Get(f.ts.URL + "/Images/fb2/" + fmt.Sprint(second.id))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled library book download = %d, want 404", resp.StatusCode)
	}
	if rating := f.users.UserRating(context.Background(), reader.ID, second.id); rating != 4 {
		t.Fatalf("personal rating after disable = %d, want retained 4", rating)
	}
	if got := managedLibraryID(t, f, "Second"); got != two {
		t.Fatalf("disabled library was removed or reidentified: %q, want %q", got, two)
	}
}

func TestLibraryRoutesRequireAdminAndAreAbsentInDemo(t *testing.T) {
	f := newFullServer(t, "required")
	if got := code(t, &http.Client{}, "GET", f.ts.URL+"/admin/libraries", ""); got != http.StatusUnauthorized {
		t.Errorf("anonymous admin library list = %d, want 401", got)
	}
	if got := code(t, f.reader, "GET", f.ts.URL+"/admin/libraries", ""); got != http.StatusForbidden {
		t.Errorf("reader admin library list = %d, want 403", got)
	}
	if got := code(t, &http.Client{}, "GET", f.ts.URL+"/api/v1/libraries", ""); got != http.StatusUnauthorized {
		t.Errorf("anonymous public library list = %d, want 401", got)
	}

	demo := newFullServer(t, "demo")
	demoClient := &http.Client{Jar: newJar(t)}
	resp, err := demoClient.Get(demo.ts.URL + "/admin/libraries")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("admin library API is registered in demo mode: %s", resp.Header.Get("Content-Type"))
	}
}
