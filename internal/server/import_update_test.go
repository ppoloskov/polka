package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vestigiumincaligne/polka/internal/store"
)

func webImportFixture(t *testing.T, title string) []byte {
	t.Helper()
	var data bytes.Buffer
	zw := zip.NewWriter(&data)
	for name, contents := range map[string]string{
		"collection.info": "Web update fixture\n",
		"books.inp":       strings.Join([]string{"Writer,Example,", "fiction", title, "Series", "1", "source-locator", "100", "1", "0", "fb2", "2024-01-02", "en", "4", "", "2020", "source-id"}, "\x04") + "\r\n",
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

func postImportForm(t *testing.T, client *http.Client, url string, fields map[string]string, filename string, contents []byte) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := mw.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if filename != "" {
		file, err := mw.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	return resp.StatusCode, result
}

func waitForImportStatus(t *testing.T, client *http.Client, url string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		var status map[string]any
		err = json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if status["running"] == false {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("web INPX import did not finish")
	return nil
}

func TestWebImportReplaceKeepsLibrariesAndReportsFailureWithoutClearing(t *testing.T) {
	ts, client, st, dir := newManageServerDir(t)
	ctx := context.Background()
	named, err := st.AddLibrary(ctx, store.Library{Name: "Named", Kind: "folder", Root: filepath.Join(dir, "named")})
	if err != nil {
		t.Fatal(err)
	}
	namedID, err := st.AddBook(ctx, &store.BookInput{LibraryID: named.ID, SourceKey: "folder:one", Title: "Named entry", Folder: "books", File: "one", Ext: "fb2"})
	if err != nil {
		t.Fatal(err)
	}
	uploadHash := sha256.Sum256([]byte("uploaded bytes"))
	uploadID, err := st.AddBook(ctx, &store.BookInput{Title: "Uploaded entry", Folder: "uploads", File: "upload", Ext: "fb2", FileHash: hex.EncodeToString(uploadHash[:])})
	if err != nil {
		t.Fatal(err)
	}

	good := webImportFixture(t, "Current source book")
	statusCode, started := postImportForm(t, client, ts.URL+"/admin/import/inpx", map[string]string{"replace": "1"}, "catalog.inpx", good)
	if statusCode != http.StatusOK || started["started"] != true {
		t.Fatalf("multipart import start = %d %v", statusCode, started)
	}
	status := waitForImportStatus(t, client, ts.URL+"/admin/import/status")
	for _, field := range []string{"running", "phase", "processed", "books", "authors", "series", "error", "warning"} {
		if _, ok := status[field]; !ok {
			t.Errorf("import status omitted legacy field %q: %v", field, status)
		}
	}
	if status["phase"] != "done" || status["books"].(float64) != 1 {
		t.Fatalf("multipart import status = %v", status)
	}
	var importedID int64
	if err := st.DB().QueryRow(`SELECT id FROM books WHERE title='Current source book' AND library_id='legacy'`).Scan(&importedID); err != nil {
		t.Fatal(err)
	}
	assertIndependent := func() {
		t.Helper()
		var count int
		if err := st.DB().QueryRow(`SELECT count(*) FROM books WHERE id=? AND library_id=?`, namedID, named.ID).Scan(&count); err != nil || count != 1 {
			t.Errorf("named library entry = %d, %v", count, err)
		}
		if err := st.DB().QueryRow(`SELECT count(*) FROM books WHERE id=? AND file_hash=? AND missing=0`, uploadID, hex.EncodeToString(uploadHash[:])).Scan(&count); err != nil || count != 1 {
			t.Errorf("hashed upload entry = %d, %v", count, err)
		}
	}
	assertIndependent()
	wantHash := sha256.Sum256(good)
	if got := st.GetMeta(ctx, "inpx_sha256", ""); got != hex.EncodeToString(wantHash[:]) {
		t.Errorf("multipart source fingerprint = %q, want %x", got, wantHash)
	}
	if got := st.GetMeta(ctx, "collection_name", ""); got != "Web update fixture" {
		t.Errorf("multipart collection name = %q, want Web update fixture", got)
	}
	var storedUploadPath string
	if err := st.DB().QueryRow(`SELECT source_path FROM libraries WHERE id='legacy'`).Scan(&storedUploadPath); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(storedUploadPath, dir+string(os.PathSeparator)) {
		t.Errorf("uploaded source path = %q, want a durable path under DataDir %q", storedUploadPath, dir)
	}
	if _, err := os.Stat(storedUploadPath); err != nil {
		t.Errorf("uploaded source was not retained: %v", err)
	}

	path := filepath.Join(dir, "mounted.inpx")
	if err := os.WriteFile(path, good, 0o600); err != nil {
		t.Fatal(err)
	}
	statusCode, started = postImportForm(t, client, ts.URL+"/admin/import/inpx", map[string]string{"replace": "1", "path": path}, "", nil)
	if statusCode != http.StatusOK || started["started"] != true {
		t.Fatalf("server-path import start = %d %v", statusCode, started)
	}
	status = waitForImportStatus(t, client, ts.URL+"/admin/import/status")
	if status["phase"] != "done" {
		t.Fatalf("server-path import status = %v", status)
	}
	var repeatedID int64
	if err := st.DB().QueryRow(`SELECT id FROM books WHERE title='Current source book' AND library_id='legacy'`).Scan(&repeatedID); err != nil || repeatedID != importedID {
		t.Errorf("repeat path import ID = %d, %v; want stable ID %d", repeatedID, err, importedID)
	}
	assertIndependent()
	priorFingerprint := st.GetMeta(ctx, "inpx_sha256", "")
	priorLibrary, err := st.Library(ctx, "legacy")
	if err != nil {
		t.Fatal(err)
	}

	corruptPath := filepath.Join(dir, "truncated.inpx")
	if err := os.WriteFile(corruptPath, []byte("not a complete archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	statusCode, started = postImportForm(t, client, ts.URL+"/admin/import/inpx", map[string]string{"replace": "1", "path": corruptPath}, "", nil)
	if statusCode != http.StatusOK || started["started"] != true {
		t.Fatalf("corrupt path import start = %d %v", statusCode, started)
	}
	status = waitForImportStatus(t, client, ts.URL+"/admin/import/status")
	if status["phase"] != "error" || status["error"] == "" {
		t.Fatalf("corrupt path import status = %v", status)
	}
	var retainedID int64
	if err := st.DB().QueryRow(`SELECT id FROM books WHERE title='Current source book' AND library_id='legacy'`).Scan(&retainedID); err != nil || retainedID != importedID {
		t.Errorf("failed replacement lost published book: id=%d err=%v want=%d", retainedID, err, importedID)
	}
	if got := st.GetMeta(ctx, "inpx_sha256", ""); got != priorFingerprint {
		t.Errorf("failed replacement changed source fingerprint from %q to %q", priorFingerprint, got)
	}
	current, err := st.Library(ctx, "legacy")
	if err != nil || current.SourcePath != priorLibrary.SourcePath {
		t.Errorf("failed replacement changed source path: %+v, %v; want %q", current, err, priorLibrary.SourcePath)
	}
	assertIndependent()
}
