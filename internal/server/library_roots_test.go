package server

import (
	"archive/zip"
	"bytes"
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

func TestExplicitLibraryRootsServeSameLocatorIndependently(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	rootA, rootB := filepath.Join(dir, "root-a"), filepath.Join(dir, "root-b")
	for root, contents := range map[string]string{
		rootA: "Alpha root text.",
		rootB: "Beta root text.",
	} {
		path := filepath.Join(root, "same", "1.txt")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	st, err := store.Open(filepath.Join(dir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	libA, err := st.AddLibrary(ctx, store.Library{ID: "alpha", Name: "Alpha", Kind: "folder", Root: rootA, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	libB, err := st.AddLibrary(ctx, store.Library{ID: "beta", Name: "Beta", Kind: "folder", Root: rootB, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	bookA := addRootTestBook(t, st, "Alpha", libA.ID)
	bookB := addRootTestBook(t, st, "Beta", libB.ID)
	legacyBook := addRootTestBook(t, st, "Legacy", "legacy")

	// No legacy root is configured. An explicit library root must still serve
	// its books, even when another library has the same catalog locator.
	ts := newLibraryRootsTestServer(t, st, dir, "")
	client := ts.Client()
	for _, tc := range []struct {
		id      int64
		content string
	}{{bookA, "Alpha root text."}, {bookB, "Beta root text."}} {
		id := itoa64(tc.id)

		resp, err := client.Get(ts.URL + "/Images/fb2/" + id)
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK || string(data) != tc.content {
			t.Errorf("download book %s = status %d, body %q, err %v; want %q", id, resp.StatusCode, data, readErr, tc.content)
		}

		resp, err = client.Get(ts.URL + "/Images/export?ids=" + id)
		if err != nil {
			t.Fatal(err)
		}
		archive, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("export book %s = status %d, err %v", id, resp.StatusCode, readErr)
		}
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil || len(zr.File) != 1 {
			t.Fatalf("export book %s: zip=%v files=%d", id, err, len(zr.File))
		}
		file, err := zr.File[0].Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr = io.ReadAll(file)
		file.Close()
		if readErr != nil || string(data) != tc.content {
			t.Errorf("export book %s contents = %q, err %v; want %q", id, data, readErr, tc.content)
		}

		resp, err = client.Get(ts.URL + "/api/v1/read/" + id + "/chapter/0")
		if err != nil {
			t.Fatal(err)
		}
		var chapter map[string]any
		decodeErr := json.NewDecoder(resp.Body).Decode(&chapter)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || decodeErr != nil || !strings.Contains(chapterString(chapter), tc.content) {
			t.Errorf("reader book %s = status %d, response %v, err %v; want %q", id, resp.StatusCode, chapter, decodeErr, tc.content)
		}
	}

	// Legacy catalog rows keep resolving through the configured library dir on
	// each server start, so an image-only upgrade keeps the existing root flag.
	for _, tc := range []struct {
		root, content string
	}{{rootA, "Alpha root text."}, {rootB, "Beta root text."}} {
		legacyServer := newLibraryRootsTestServer(t, st, dir, tc.root)
		resp, err := legacyServer.Client().Get(legacyServer.URL + "/Images/fb2/" + itoa64(legacyBook))
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		legacyServer.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK || string(data) != tc.content {
			t.Errorf("legacy root %q download = status %d, body %q, err %v; want %q", tc.root, resp.StatusCode, data, readErr, tc.content)
		}
	}
}

func addRootTestBook(t *testing.T, st *store.Store, title, libraryID string) int64 {
	t.Helper()
	id, err := st.AddBook(context.Background(), &store.BookInput{Title: title, Folder: "same", File: "1", Ext: "txt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`UPDATE books SET library_id = ? WHERE id = ?`, libraryID, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func newLibraryRootsTestServer(t *testing.T, st *store.Store, dir, libraryDir string) *httptest.Server {
	t.Helper()
	users, err := auth.Open(filepath.Join(dir, "users.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { users.Close() })
	cfg := &config.Config{LibraryDir: libraryDir, DataDir: dir, Auth: "public"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	webFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html/>")}}
	ts := httptest.NewServer(New(cfg, log, st, library.New(libraryDir), users, nil, webFS).Handler)
	t.Cleanup(ts.Close)
	return ts
}

func chapterString(chapter map[string]any) string {
	value, _ := chapter["html"].(string)
	return value
}
