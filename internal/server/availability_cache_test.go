package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestReaderCacheDoesNotServeMissingBook(t *testing.T) {
	ts, client, st := newManageServer(t)
	resp := uploadFiles(t, client, ts.URL+"/admin/books/upload", map[string][]byte{
		"cached-reader.fb2": []byte(readerFB2),
	}, nil)
	var uploaded map[string][]uploadResult
	if err := json.NewDecoder(resp.Body).Decode(&uploaded); err != nil {
		resp.Body.Close()
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(uploaded["results"]) != 1 || uploaded["results"][0].BookID == 0 {
		t.Fatalf("upload = status %d, result %+v", resp.StatusCode, uploaded)
	}
	bookID := uploaded["results"][0].BookID
	chapterURL := ts.URL + "/api/v1/read/" + itoa64(bookID) + "/chapter/0"

	resp, err := client.Get(chapterURL)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(first), "Текст первой главы") {
		t.Fatalf("initial reader chapter = status %d, body %q", resp.StatusCode, first)
	}

	if _, err := st.DB().Exec(`UPDATE books SET missing = 1 WHERE id = ?`, bookID); err != nil {
		t.Fatal(err)
	}
	resp, err = client.Get(chapterURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("cached chapter after removal = status %d, body %q; want 404", resp.StatusCode, body)
	}
}
