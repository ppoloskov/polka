package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFB2BidirectionalResume(t *testing.T) {
	ts, client, _ := newManageServer(t)
	var uploaded map[string][]uploadResult
	decode(t, uploadFiles(t, client, ts.URL+"/admin/books/upload", map[string][]byte{"reader.fb2": []byte(readerFB2)}, nil), &uploaded)
	id := itoa64(uploaded["results"][0].BookID)
	path := ts.URL + "/api/v1/read/" + id
	postJSON(t, client, ts.URL+"/api/v1/me/kosync", map[string]string{"password": "device-pass-123"}).Body.Close()
	key := md5hex("device-pass-123")
	// Opening the web reader registers the digest, without any file download.
	meta := getJSONWith(t, client, path)
	digest, ok := meta["digest"].(string)
	if !ok || len(digest) != 32 {
		t.Fatalf("digest: %v", meta)
	}
	deviceLocator := "/FictionBook/body[1]/section[2]/p/text().5"
	push, _ := json.Marshal(map[string]any{"document": digest, "progress": deviceLocator, "percentage": 0.7, "device": "KOReader", "device_id": "test-device"})
	if status, body := kosyncDo(t, ts.URL, "PUT", "/syncs/progress", "admin", key, string(push)); status != 200 {
		t.Fatalf("push: %d %v", status, body)
	}
	p := getJSONWith(t, client, path+"/progress")
	if p["chapter"] != float64(1) || !strings.HasSuffix(p["locator"].(string), "/section[2]/p[1]/text()[1].5") || p["progress"] != 0.7 {
		t.Fatalf("device -> web: %v", p)
	}
	revision := p["revision"]
	// A deliberate move backwards in the browser must reach KOReader.
	webLocator := "/FictionBook[1]/body[1]/section[1]/p[1]/text()[1].3"
	resp := postJSON(t, client, path+"/progress", map[string]any{"chapter": 0, "position": 0.1, "progress": 0.2, "locator": webLocator, "revision": revision, "digest": digest})
	if resp.StatusCode != 200 {
		t.Fatalf("web save: %d", resp.StatusCode)
	}
	resp.Body.Close()
	_, ko := kosyncDo(t, ts.URL, "GET", "/syncs/progress/"+digest, "admin", key, "")
	if ko["progress"] != webLocator || ko["device_id"] != "polka-web" {
		t.Fatalf("web -> device: %v", ko)
	}
	// An old tab cannot overwrite either side of the atomic bridge.
	resp = postJSON(t, client, path+"/progress", map[string]any{"locator": webLocator, "revision": revision, "digest": digest})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("stale save: %d", resp.StatusCode)
	}
	resp.Body.Close()
	_, after := kosyncDo(t, ts.URL, "GET", "/syncs/progress/"+digest, "admin", key, "")
	if after["progress"] != ko["progress"] || after["timestamp"] != ko["timestamp"] {
		t.Errorf("stale save changed kosync: %v", after)
	}
	// Another file identity must never receive this file's xpointer.
	resp = postJSON(t, client, path+"/progress", map[string]any{"locator": webLocator, "digest": strings.Repeat("a", 32)})
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("wrong fingerprint: %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = postJSON(t, client, path+"/progress", map[string]any{"locator": "/FictionBook/body/section[99]/p/text().0", "digest": digest})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid location: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// A book downloaded through OPDS goes out as.fb2.zip: its INNER file bytes
// must be digested and recorded exactly like a raw download, or the device
// reading that file cannot sync positions with the web reader at all.
func TestOPDSZipDownloadBridgesKosync(t *testing.T) {
	ts, client, st := newManageServer(t)
	var uploaded map[string][]uploadResult
	decode(t, uploadFiles(t, client, ts.URL+"/admin/books/upload", map[string][]byte{"reader.fb2": []byte(readerFB2)}, nil), &uploaded)
	id := itoa64(uploaded["results"][0].BookID)
	postJSON(t, client, ts.URL+"/api/v1/me/kosync", map[string]string{"password": "device-pass-123"}).Body.Close()
	key := md5hex("device-pass-123")

	// The stored file's digest (what the web reader keys its positions by).
	meta := getJSONWith(t, client, ts.URL+"/api/v1/read/"+id)
	storedDigest, ok := meta["digest"].(string)
	if !ok || len(storedDigest) != 32 {
		t.Fatalf("digest: %v", meta)
	}

	// Download through the OPDS acquisition link. The inner entry bytes are
	// what KOReader digests after unzipping.
	resp, err := client.Get(ts.URL + "/Images/zip/" + id)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("zip download: %d", resp.StatusCode)
	}
	innerBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(innerBody), int64(len(innerBody)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 {
		t.Fatalf("zip entries = %d; want one fb2 file", len(zr.File))
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	tee := &digestTee{}
	if _, err := io.Copy(tee, rc); err != nil {
		t.Fatal(err)
	}
	rc.Close()
	deviceDigest := tee.digest()
	if deviceDigest == storedDigest && !strings.Contains(string(innerBody), "<binary") {
		// Only a re-embedding makes the digests diverge; the fixture may be
		// complete already.
		t.Logf("device digest equals the stored digest")
	}
	if _, err := st.BookIDByDigest(t.Context(), deviceDigest); err != nil {
		t.Fatalf("device digest was not recorded: %v", err)
	}

	// The web reader moves its position.
	webLocator := "/FictionBook[1]/body[1]/section[1]/p[1]/text()[1].9"
	resp = postJSON(t, client, ts.URL+"/api/v1/read/"+id+"/progress", map[string]any{
		"chapter": 0, "position": 0.2, "progress": 0.25, "locator": webLocator, "digest": storedDigest,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("web save: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// The device pulls for ITS digest: it receives the web reader's
	// position (the body text is identical across the served variants).
	_, ko := kosyncDo(t, ts.URL, "GET", "/syncs/progress/"+deviceDigest, "admin", key, "")
	if ko["progress"] != webLocator || ko["device_id"] != "polka-web" {
		t.Fatalf("web -> device bridge: %v", ko)
	}

	// The device reads on and pushes its own FB2 location: it bridges back
	// to the web position even though its file digest differs from the
	// stored file (the served variant only appends <binary> elements).
	deviceLocator := "/FictionBook/body[1]/section[1]/p/text().10"
	push, _ := json.Marshal(map[string]any{
		"document": deviceDigest, "progress": deviceLocator, "percentage": 0.5, "device": "KOReader", "device_id": "test-device",
	})
	if status, body := kosyncDo(t, ts.URL, "PUT", "/syncs/progress", "admin", key, string(push)); status != 200 {
		t.Fatalf("device push: %d %v", status, body)
	}
	p := getJSONWith(t, client, ts.URL+"/api/v1/read/"+id+"/progress")
	if p["chapter"] != float64(0) || !strings.Contains(p["locator"].(string), "/section[1]/p[1]/text()[1].10") {
		t.Fatalf("device -> web bridge: %v", p)
	}
	if p["progress"] != 0.5 {
		t.Fatalf("bridged progress = %v; want the device percentage", p["progress"])
	}
}
