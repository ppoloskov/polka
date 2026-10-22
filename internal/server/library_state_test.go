package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vestigiumincaligne/polka/internal/importer"
	"github.com/vestigiumincaligne/polka/internal/store"
)

func TestIdenticalLibraryCopiesKeepIndependentWebProgress(t *testing.T) {
	f := newFullServer(t, "required")
	ctx := t.Context()
	var ids []int64
	for _, name := range []string{"Copy A", "Copy B"} {
		root := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "same.fb2"), []byte(readerFB2), 0o644); err != nil {
			t.Fatal(err)
		}
		library, err := f.st.AddLibrary(ctx, store.Library{Name: name, Kind: "folder", Root: root})
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := importer.StageFolder(ctx, root, f.dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.st.PublishLibrary(ctx, library.ID, snapshot, false); err != nil {
			snapshot.Close()
			t.Fatal(err)
		}
		if err := snapshot.Close(); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := f.st.DB().QueryRow(`SELECT id FROM books WHERE library_id=?`, library.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if ids[0] == ids[1] {
		t.Fatalf("identical source copies share catalog ID %d", ids[0])
	}
	metadata := make([]map[string]any, 2)
	for i, id := range ids {
		metadata[i] = getJSONWith(t, f.reader, f.ts.URL+"/api/v1/read/"+itoa64(id))
	}
	if metadata[0]["digest"] == "" || metadata[0]["digest"] != metadata[1]["digest"] {
		t.Fatalf("identical copies should expose the same reader digest: %v / %v", metadata[0]["digest"], metadata[1]["digest"])
	}
	for i, progress := range []float64{0.2, 0.8} {
		resp := postJSON(t, f.reader, f.ts.URL+"/api/v1/read/"+itoa64(ids[i])+"/progress", map[string]any{"chapter": 0, "position": progress, "progress": progress})
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("save progress for book %d: HTTP %d", ids[i], resp.StatusCode)
		}
	}
	for i, want := range []float64{0.2, 0.8} {
		got := getJSONWith(t, f.reader, f.ts.URL+"/api/v1/read/"+itoa64(ids[i])+"/progress")
		if got["position"] != want || got["progress"] != want {
			t.Errorf("book %d progress = %v; want independent position %v", ids[i], got, want)
		}
	}
}
