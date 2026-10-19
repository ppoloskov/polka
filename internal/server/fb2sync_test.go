package server

import (
	"encoding/json"
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
