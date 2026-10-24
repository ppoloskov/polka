package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/vestigiumincaligne/polka/internal/auth"
	"github.com/vestigiumincaligne/polka/internal/library"
)

// readerCache keeps the last few parsed books in memory:
// a reader navigates between chapters of one book, so there is no need to re-parse the file.
type readerCache struct {
	mu      sync.Mutex
	entries map[int64]*library.FB2Text
	order   []int64
}

const readerCacheSize = 8

func (c *readerCache) get(id int64) *library.FB2Text {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries[id]
}

func (c *readerCache) put(id int64, text *library.FB2Text) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[int64]*library.FB2Text, readerCacheSize)
	}
	if _, exists := c.entries[id]; exists {
		return
	}
	for len(c.order) >= readerCacheSize {
		delete(c.entries, c.order[0])
		c.order = c.order[1:]
	}
	c.entries[id] = text
	c.order = append(c.order, id)
}

// bookText returns the parsed book (from the cache or from disk).
func (s *Server) bookText(r *http.Request, bookID int64) (*library.FB2Text, error) {
	f, err := s.st.BookFile(r.Context(), bookID)
	if err != nil {
		return nil, err
	}
	if text := s.reader.get(bookID); text != nil {
		return text, nil
	}
	lib, err := s.bookLibrary(r.Context(), bookID)
	if err != nil {
		return nil, err
	}
	imgURL := func(id string) string {
		return fmt.Sprintf("/api/v1/read/%d/img/%s", bookID, id)
	}
	var text *library.FB2Text
	if strings.EqualFold(f.Ext, "fb2") {
		rc, size, openErr := lib.Open(f.Folder, f.File, f.Ext)
		if openErr != nil {
			return nil, openErr
		}
		defer rc.Close()
		tee := &digestTee{}
		text, err = library.ParseFB2Text(io.TeeReader(rc, tee), imgURL)
		if err == nil {
			// Parsing stops at <binary>; drain the rest for the file identity.
			_, err = io.Copy(tee, rc)
			if err == nil && tee.pos == size {
				text.Digest = tee.digest()
				err = s.st.SetBookDigest(r.Context(), text.Digest, bookID)
			}
		}
	} else {
		text, err = lib.Text(f.Folder, f.File, f.Ext, imgURL)
	}
	if err != nil {
		return nil, err
	}
	s.reader.put(bookID, text)
	return text, nil
}

func (s *Server) readerBookID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

// GET /api/v1/read/{id} — table of contents and footnotes.
func (s *Server) handleReadMeta(w http.ResponseWriter, r *http.Request) {
	bookID, ok := s.readerBookID(w, r)
	if !ok {
		return
	}
	d, err := s.st.BookDetails(r.Context(), bookID)
	if err != nil {
		s.apiError(w, err)
		return
	}

	format := strings.ToLower(d.Ext)
	switch format {
	case "fb2", "txt", "epub":
		// chapters are rendered by our reader — continue below
	case "pdf":
		// rendered by a browser engine (pdf.js) from the file
		writeJSON(w, map[string]any{
			"bookId":  bookID,
			"format":  format,
			"title":   d.Title,
			"authors": d.AuthorNames,
			"fileUrl": fmt.Sprintf("/Images/fb2/%d?inline=1", bookID),
		})
		return
	default:
		http.Error(w, "online reading is not available for this format", http.StatusUnsupportedMediaType)
		return
	}

	text, err := s.bookText(r, bookID)
	if err != nil {
		s.log.Warn("read book failed", "book", bookID, "error", err)
		s.apiError(w, err)
		return
	}

	lang := reqLang(r)
	chapters := make([]map[string]any, 0, len(text.Chapters))
	for i, ch := range text.Chapters {
		title := ch.Title
		if title == "" {
			if i == 0 {
				title = tr(lang, "reader.start")
			} else {
				title = tr(lang, "reader.chapter", i+1)
			}
		}
		chapters = append(chapters, map[string]any{"index": i, "title": title, "size": len(ch.HTML)})
	}
	writeJSON(w, map[string]any{
		"bookId":   bookID,
		"format":   format,
		"title":    d.Title,
		"authors":  d.AuthorNames,
		"lang":     d.Lang,
		"chapters": chapters,
		"notes":    text.Notes,
		"digest":   text.Digest,
	})
}

// GET /api/v1/read/{id}/chapter/{n} — HTML of a single chapter.
func (s *Server) handleReadChapter(w http.ResponseWriter, r *http.Request) {
	bookID, ok := s.readerBookID(w, r)
	if !ok {
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	text, err := s.bookText(r, bookID)
	if err != nil {
		s.log.Warn("read book failed", "book", bookID, "error", err)
		s.apiError(w, err)
		return
	}
	if n < 0 || n >= len(text.Chapters) {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, map[string]any{
		"index":     n,
		"total":     len(text.Chapters),
		"title":     text.Chapters[n].Title,
		"titleHtml": text.Chapters[n].TitleHTML,
		"html":      text.Chapters[n].HTML,
	})
}

// GET /api/v1/read/{id}/search?q= — find the query in the book text.
// Chapters are scanned in reading order from the parsed book cache;
// matches report the chapter, the occurrence count and one snippet. The
// client jumps to the chapter and highlights occurrences there.
func (s *Server) handleReadSearch(w http.ResponseWriter, r *http.Request) {
	bookID, ok := s.readerBookID(w, r)
	if !ok {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len(query) > 100 {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	text, err := s.bookText(r, bookID)
	if err != nil {
		s.log.Warn("read book failed", "book", bookID, "error", err)
		s.apiError(w, err)
		return
	}
	needle := strings.ToLower(query)
	matches := make([]map[string]any, 0, 4)
	total := 0
	for i, ch := range text.Chapters {
		plain, lower := chapterPlainText(ch.HTML)
		count, snippet := countMatches(lower, needle, plain)
		if count == 0 {
			continue
		}
		title := ch.Title
		if title == "" {
			title = tr(reqLang(r), "reader.chapter", i+1)
		}
		matches = append(matches, map[string]any{
			"index":   i,
			"title":   title,
			"count":   count,
			"snippet": snippet,
		})
		total += count
	}
	writeJSON(w, map[string]any{"matches": matches, "total": total})
}

// chapterPlainText strips tags and decodes the few entities the converter
// emits, returning usable text plus its lowercase form for searching.
func chapterPlainText(html string) (text, lower string) {
	var sb strings.Builder
	sb.Grow(len(html))
	for i := 0; i < len(html); {
		if html[i] == '<' {
			if j := strings.IndexByte(html[i:], '>'); j >= 0 {
				i += j + 1
				sb.WriteByte(' ')
				continue
			}
			break
		}
		sb.WriteByte(html[i])
		i++
	}
	text = strings.Join(strings.Fields(sb.String()), " ")
	replacer := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ")
	text = replacer.Replace(text)
	return text, strings.ToLower(text)
}

// countMatches counts non-overlapping case-insensitive occurrences and
// builds one contextual snippet around the first match.
func countMatches(lower, needle, plain string) (int, string) {
	count := 0
	first := -1
	for i := 0; ; {
		j := strings.Index(lower[i:], needle)
		if j < 0 {
			break
		}
		at := i + j
		if first < 0 {
			first = at
		}
		count++
		i = at + len(needle)
	}
	if count == 0 {
		return 0, ""
	}
	const ctx = 48
	lo, hi := first-ctx, first+len(needle)+ctx
	for lo > 0 && plain[lo] != ' ' {
		lo--
	}
	if hi > len(plain) {
		hi = len(plain)
	} else {
		for hi < len(plain) && plain[hi] != ' ' {
			hi++
		}
	}
	if lo > 0 {
		lo++
	}
	return count, strings.TrimSpace(plain[lo:hi])
}

// GET/POST/DELETE /api/v1/read/{id}/bookmarks — saved positions of one
// book for the current user.
func (s *Server) handleReadBookmarks(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if u == nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	bookID, ok := s.readerBookID(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		bookmarks, err := s.users.Bookmarks(r.Context(), u.ID, bookID)
		if err != nil {
			s.apiError(w, err)
			return
		}
		writeJSON(w, map[string]any{"bookmarks": bookmarks})
	case http.MethodPost:
		var req struct {
			Chapter  int     `json:"chapter"`
			Position float64 `json:"position"`
			Locator  string  `json:"locator"`
			Label    string  `json:"label"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if req.Chapter < 0 {
			http.Error(w, "invalid chapter", http.StatusBadRequest)
			return
		}
		if req.Position < 0 || req.Position > 1 {
			req.Position = 0
		}
		if len(req.Label) > 200 {
			req.Label = req.Label[:200]
		}
		b, err := s.users.AddBookmark(r.Context(), u.ID, bookID, req.Chapter, req.Position, req.Locator, req.Label)
		if err != nil {
			s.apiError(w, err)
			return
		}
		writeJSON(w, map[string]any{"bookmark": b})
	case http.MethodDelete:
		id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid id", http.StatusBadRequest)
			return
		}
		if err := s.users.DeleteBookmark(r.Context(), u.ID, id); err != nil {
			s.apiError(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// GET /api/v1/read/{id}/img/{imgId} — an illustration from the book.
func (s *Server) handleReadImage(w http.ResponseWriter, r *http.Request) {
	bookID, ok := s.readerBookID(w, r)
	if !ok {
		return
	}
	imgID := r.PathValue("imgId")
	f, err := s.st.BookFile(r.Context(), bookID)
	if err != nil {
		s.apiError(w, err)
		return
	}
	lib, err := s.bookLibrary(r.Context(), bookID)
	if err != nil {
		s.apiError(w, err)
		return
	}
	data, mime, err := lib.Binary(f.Folder, f.File, f.Ext, imgID)
	if err != nil {
		s.apiError(w, err)
		return
	}
	if mime == "" {
		mime = "image/jpeg"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(data)
}

// GET/POST /api/v1/read/{id}/progress — the user's reading position.
func (s *Server) handleReadProgress(w http.ResponseWriter, r *http.Request) {
	// Progress is user data: it does not need the library (sync-mode
	// clients read proxied books without one).
	bookID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	u := s.currentUser(r)
	if u == nil {
		// Public mode without login: the position is stored in the browser.
		writeJSON(w, map[string]any{"stored": false})
		return
	}

	if r.Method == http.MethodPost {
		var p auth.Progress
		var revision *int64
		var digest string
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&struct {
			Chapter  *int     `json:"chapter"`
			Position *float64 `json:"position"`
			Overall  *float64 `json:"progress"`
			Locator  *string  `json:"locator"`
			Revision **int64  `json:"revision"`
			Digest   *string  `json:"digest"`
		}{&p.Chapter, &p.Position, &p.Overall, &p.Locator, &revision, &digest}); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if p.Position < 0 || p.Position > 1 || p.Overall < 0 || p.Overall > 1 || p.Chapter < 0 {
			http.Error(w, "invalid progress", http.StatusBadRequest)
			return
		}
		var ko *auth.KosyncProgress
		if p.Locator != "" && strings.HasPrefix(p.Locator, "/FictionBook") {
			if _, err := s.bookLibrary(r.Context(), bookID); err == nil {
				text, err := s.bookText(r, bookID)
				if err != nil {
					s.apiError(w, err)
					return
				}
				if digest == "" || digest != text.Digest {
					http.Error(w, "book file changed; reopen the reader", http.StatusPreconditionFailed)
					return
				}
				locator, chapter, position, overall, ok := text.ResolveFB2Locator(p.Locator)
				if !ok {
					http.Error(w, "invalid FB2 location", http.StatusBadRequest)
					return
				}
				p.Locator, p.Chapter, p.Position, p.Overall = storeFB2Position(digest, locator), chapter, position, overall
				ko = &auth.KosyncProgress{Document: digest, Progress: locator, Percentage: overall, Device: "Polka web reader", DeviceID: "polka-web"}
			} else if !errors.Is(err, errLibraryDirNotConfigured) {
				s.apiError(w, err)
				return
			}
		}
		savedRevision, err := s.users.SaveBridgedProgress(r.Context(), u.ID, bookID, p, revision, ko)
		if errors.Is(err, auth.ErrProgressConflict) {
			http.Error(w, "reading position changed", http.StatusConflict)
			return
		}
		if err != nil {
			s.apiError(w, err)
			return
		}
		writeJSON(w, map[string]any{"stored": true, "revision": savedRevision})
		return
	}

	p, err := s.users.GetProgress(r.Context(), u.ID, bookID)
	if err != nil {
		writeJSON(w, map[string]any{"stored": true, "chapter": 0, "position": 0, "progress": 0, "revision": 0})
		return
	}
	document, locator := readFB2Position(p.Locator)
	writeJSON(w, map[string]any{
		"stored": true, "chapter": p.Chapter, "position": p.Position,
		"progress": p.Overall, "locator": locator, "document": document, "revision": p.Revision,
	})
}
