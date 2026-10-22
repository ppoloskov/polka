package server

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/vestigiumincaligne/polka/internal/genres"
	"github.com/vestigiumincaligne/polka/internal/library"
	"github.com/vestigiumincaligne/polka/internal/store"
)

// bookJSON converts a book to the format expected by the frontend.
func bookJSON(b store.Book) map[string]any {
	return map[string]any{
		"BookID":       b.ID,
		"Title":        b.Title,
		"AuthorsNames": b.AuthorNames,
		"SeriesTitle":  b.SeriesTitle,
		"SeqNumber":    b.SeqNumber,
		"Year":         b.Year,
		"LibRate":      b.LibRate,
		"LibraryID":    b.LibraryID,
		"LibraryName":  b.LibraryName,
		"Available":    b.Available,
	}
}

func booksJSON(books []store.Book) []map[string]any {
	out := make([]map[string]any, 0, len(books))
	for _, b := range books {
		out = append(out, bookJSON(b))
	}
	return out
}

func shelvesJSON(shelves []store.Shelf) []map[string]any {
	out := make([]map[string]any, 0, len(shelves))
	for _, sh := range shelves {
		out = append(out, map[string]any{
			"id":      sh.ID,
			"title":   sh.Title,
			"books":   booksJSON(sh.Books),
			"hasMore": sh.HasMore,
		})
	}
	return out
}

func intParam(r *http.Request, name string, def int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(name)); err == nil && v > 0 {
		return v
	}
	return def
}

func idParam(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
	return id, err == nil && id > 0
}

// searchParam: URL parameters encode a space as '+'.
func searchParam(r *http.Request) string {
	return strings.TrimSpace(strings.ReplaceAll(r.URL.Query().Get("search"), "+", " "))
}

func (s *Server) apiError(w http.ResponseWriter, err error) {
	if errors.Is(err, errLibraryDirNotConfigured) {
		http.Error(w, "library dir is not configured", http.StatusNotFound)
		return
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, library.ErrNoFile) {
		http.NotFound(w, nil)
		return
	}
	s.log.Error("api", "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// --- main/getBooks/* ---

func (s *Server) handleGetHomeShelves(w http.ResponseWriter, r *http.Request) {
	limit := intParam(r, "limit", 12)
	libraryID := r.URL.Query().Get("libraryId")
	shelves, err := s.st.HomeShelves(r.Context(), limit, libraryID)
	if err != nil {
		s.apiError(w, err)
		return
	}
	out := shelvesJSON(shelves)

	// Personal shelves on top: "Reading now", "Want to read",
	// then recommendations ("Continue series", "For you").
	if u := s.currentUser(r); u != nil {
		lang := reqLang(r)
		var personal []map[string]any
		if reading := s.readingShelf(r, u.ID, limit, libraryID); len(reading) > 0 {
			personal = append(personal, map[string]any{
				"id": "reading", "title": tr(lang, "shelf.reading"), "books": reading, "hasMore": false,
			})
		}
		if wishlist := s.wishlistShelf(r, u.ID, limit, libraryID); wishlist != nil {
			wishlist["title"] = tr(lang, "shelf.wishlist")
			personal = append(personal, wishlist)
		}
		personal = append(personal, s.recShelves(r, u.ID, limit, libraryID)...)
		out = append(personal, out...)
	}
	// Collections ("100 books by Forbes"…) go between personal and shared shelves.
	if cs := s.collectionShelves(r, limit, libraryID); len(cs) > 0 {
		split := len(out) - len(shelves)
		out = append(append(append([]map[string]any{}, out[:split]...), cs...), out[split:]...)
	}
	writeJSON(w, map[string]any{"shelves": out})
}

// wishlistShelf is the "Want to read" shelf, if it has any books.
func (s *Server) wishlistShelf(r *http.Request, userID int64, limit int, libraryID string) map[string]any {
	wl, err := s.users.Wishlist(r.Context(), userID)
	if err != nil {
		return nil
	}
	refLimit := limit + 1
	if libraryID != "" {
		refLimit = -1
	}
	ids, err := s.users.ListBookIDs(r.Context(), userID, wl.ID, refLimit, 0)
	if err != nil {
		return nil
	}
	books, err := s.st.PersonalBooksByIDs(r.Context(), ids, libraryID)
	if err != nil || len(books) == 0 {
		return nil
	}
	hasMore := len(books) > limit
	if hasMore {
		books = books[:limit]
	}
	return map[string]any{
		"id": "wishlist", "title": wl.Name, "books": booksJSON(books), "hasMore": hasMore,
	}
}

// readingShelf returns books from reading progress with the fraction read.
func (s *Server) readingShelf(r *http.Request, userID int64, limit int, libraryID string) []map[string]any {
	if limit <= 0 {
		return nil
	}
	refLimit := limit
	if libraryID != "" {
		refLimit = -1
	}
	progress, err := s.users.ListProgress(r.Context(), userID, refLimit)
	if err != nil || len(progress) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(progress))
	overall := make(map[int64]float64)
	for _, p := range progress {
		ids = append(ids, p.BookID)
		overall[p.BookID] = p.Overall
	}
	books, err := s.st.PersonalBooksByIDs(r.Context(), ids, libraryID)
	if err != nil {
		s.log.Warn("reading shelf", "error", err)
		return nil
	}
	if len(books) > limit {
		books = books[:limit]
	}
	out := make([]map[string]any, 0, len(books))
	for _, b := range books {
		j := bookJSON(b)
		j["ReadingProgress"] = overall[b.ID]
		out = append(out, j)
	}
	return out
}

func (s *Server) handleGetCatalogShelves(w http.ResponseWriter, r *http.Request) {
	shelves, err := s.st.CatalogShelves(r.Context(), intParam(r, "count", 6), intParam(r, "limit", 12), r.URL.Query().Get("libraryId"))
	if err != nil {
		s.apiError(w, err)
		return
	}
	lang := reqLang(r)
	for i := range shelves {
		shelves[i].Title = genres.NameLang(strings.TrimPrefix(shelves[i].Title, "genre_"), lang)
	}
	writeJSON(w, map[string]any{"shelves": shelvesJSON(shelves)})
}

func (s *Server) handleGetShelfBooks(w http.ResponseWriter, r *http.Request) {
	shelfID := r.URL.Query().Get("shelfId")
	limit := intParam(r, "limit", 60)
	offset := 0
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
		offset = v
	}
	var books []store.Book
	var title string
	var err error
	libraryID := r.URL.Query().Get("libraryId")
	if slug, ok := strings.CutPrefix(shelfID, collectionShelfPrefix); ok {
		books, title, err = s.collectionShelfBooks(r.Context(), slug, limit+1, offset, libraryID)
	} else {
		books, title, err = s.st.ShelfBooks(r.Context(), shelfID, limit+1, offset, libraryID)
	}
	if err != nil {
		s.apiError(w, err)
		return
	}
	hasMore := len(books) > limit
	if hasMore {
		books = books[:limit]
	}
	if strings.HasPrefix(shelfID, "genre_") {
		title = genres.NameLang(strings.TrimPrefix(shelfID, "genre_"), reqLang(r))
	}
	writeJSON(w, map[string]any{
		"titlesList": booksJSON(books),
		"title":      title,
		"hasMore":    hasMore,
		"nextOffset": offset + len(books),
	})
}

// GET /main/getBooks/getBooksByIds?ids=1,2,3 — book cards by id
// (used by desktop clients for books from lists).
func (s *Server) handleGetBooksByIDs(w http.ResponseWriter, r *http.Request) {
	var ids []int64
	for _, part := range strings.Split(r.URL.Query().Get("ids"), ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || len(ids) > 500 {
		http.Error(w, "ids: from 1 to 500 book ids", http.StatusBadRequest)
		return
	}
	books, err := s.st.BooksByIDs(r.Context(), ids)
	if err != nil {
		s.apiError(w, err)
		return
	}
	writeJSON(w, map[string]any{"titlesList": booksJSON(books)})
}

func (s *Server) handleGetSearchStats(w http.ResponseWriter, r *http.Request) {
	query := searchParam(r)
	libraryID := r.URL.Query().Get("libraryId")
	if store.LooksLikeISBN(query) {
		writeJSON(w, map[string]any{"searchStats": map[string]int{
			"bookTitles": len(s.isbnBooks(r, query, libraryID)), "authors": 0, "bookSeries": 0, "genres": 0,
		}})
		return
	}
	stats, err := s.st.SearchStats(r.Context(), query, libraryID)
	if err != nil {
		s.apiError(w, err)
		return
	}
	writeJSON(w, map[string]any{"searchStats": map[string]int{
		"bookTitles": stats.BookTitles,
		"authors":    stats.Authors,
		"bookSeries": stats.BookSeries,
		"genres":     len(s.searchGenres(r, query, libraryID)),
	}})
}

func (s *Server) handleGetSearchTitles(w http.ResponseWriter, r *http.Request) {
	query := searchParam(r)
	libraryID := r.URL.Query().Get("libraryId")
	if store.LooksLikeISBN(query) {
		writeJSON(w, map[string]any{"titlesList": booksJSON(s.isbnBooks(r, query, libraryID))})
		return
	}
	books, err := s.st.SearchTitles(r.Context(), query, 250, libraryID)
	if err != nil {
		s.apiError(w, err)
		return
	}
	writeJSON(w, map[string]any{"titlesList": booksJSON(books)})
}

func (s *Server) handleGetSearchAuthors(w http.ResponseWriter, r *http.Request) {
	authors, err := s.st.SearchAuthors(r.Context(), searchParam(r), 250, r.URL.Query().Get("libraryId"))
	if err != nil {
		s.apiError(w, err)
		return
	}
	list := make([]map[string]any, 0, len(authors))
	for _, a := range authors {
		list = append(list, map[string]any{"AuthorID": a.ID, "Authors": a.Name, "Books": a.Books})
	}
	writeJSON(w, map[string]any{"authorsList": list})
}

func (s *Server) handleGetSearchSeries(w http.ResponseWriter, r *http.Request) {
	series, err := s.st.SearchSeries(r.Context(), searchParam(r), 250, r.URL.Query().Get("libraryId"))
	if err != nil {
		s.apiError(w, err)
		return
	}
	list := make([]map[string]any, 0, len(series))
	for _, e := range series {
		list = append(list, map[string]any{"SeriesID": e.ID, "SeriesTitle": e.Title, "Books": e.Books})
	}
	writeJSON(w, map[string]any{"seriesList": list})
}

func (s *Server) handleGetAuthorBooks(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "selectedItemID")
	if !ok {
		http.Error(w, "selectedItemID required", http.StatusBadRequest)
		return
	}
	books, name, err := s.st.AuthorBooks(r.Context(), id, r.URL.Query().Get("libraryId"))
	if err != nil {
		s.apiError(w, err)
		return
	}
	writeJSON(w, map[string]any{"titlesList": booksJSON(books), "title": name})
}

func (s *Server) handleGetSeriesBooks(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "selectedItemID")
	if !ok {
		http.Error(w, "selectedItemID required", http.StatusBadRequest)
		return
	}
	books, title, err := s.st.SeriesBooks(r.Context(), id, r.URL.Query().Get("libraryId"))
	if err != nil {
		s.apiError(w, err)
		return
	}
	writeJSON(w, map[string]any{"titlesList": booksJSON(books), "title": title})
}

func (s *Server) handleGetBookForm(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "selectedItemID")
	if !ok {
		http.Error(w, "selectedItemID required", http.StatusBadRequest)
		return
	}
	d, err := s.st.BookDetails(r.Context(), id)
	if err != nil {
		s.apiError(w, err)
		return
	}

	genreNames := make([]string, 0, len(d.Genres))
	genreList := make([]map[string]any, 0, len(d.Genres))
	for _, code := range d.Genres {
		name := genres.NameLang(code, reqLang(r))
		genreNames = append(genreNames, name)
		genreList = append(genreList, map[string]any{"code": code, "name": name})
	}

	authors := make([]map[string]any, 0, len(d.Authors))
	for _, a := range d.Authors {
		authors = append(authors, map[string]any{
			"AuthorID": a.ID,
			"LastName": a.Last, "FirstName": a.First, "MiddleName": a.Middle,
		})
	}

	resp := map[string]any{
		"bookForm": map[string]any{
			"BookID":       d.ID,
			"Title":        d.Title,
			"AuthorsNames": d.AuthorNames,
			"LibRate":      d.LibRate,
			"BookSize":     d.Size,
			"Genres":       strings.Join(genreNames, ", "),
			"Ext":          "." + d.Ext,
			"FileName":     d.File,
			"LibraryID":    d.LibraryID,
			"LibraryName":  d.LibraryName,
			"Available":    d.Available,
		},
		"authors":    authors,
		"genresList": genreList,
		"annotation": "",
	}

	// Polka's internal ratings: overall and the current user's.
	if avg, count, err := s.users.BookRating(r.Context(), d.ID); err == nil {
		resp["polkaRating"] = map[string]any{"rating": avg, "count": count}
	}
	if u := s.currentUser(r); u != nil {
		resp["userRating"] = s.users.UserRating(r.Context(), u.ID, d.ID)
		if listIDs, err := s.users.BookListIDs(r.Context(), u.ID, d.ID); err == nil {
			resp["bookListIds"] = listIDs
		}
	}

	if lib, err := s.bookLibrary(r.Context(), d.ID); err == nil {
		if meta, err := lib.Meta(d.Folder, d.File, d.Ext); err == nil {
			resp["annotation"] = meta.AnnotationHTML
			resp["publisher"] = meta.Publisher
			resp["city"] = meta.City
			resp["year"] = meta.Year
			resp["isbn"] = meta.ISBN
			// Lazy indexing: the ISBN from the file becomes searchable.
			s.st.SetBookISBN(r.Context(), d.ID, meta.ISBN)
			if len(meta.Sequences) > 0 {
				var refs []store.SeriesRef
				for _, sequence := range meta.Sequences {
					refs = append(refs, store.SeriesRef{Title: sequence.Name, SeqNumber: sequence.Number})
				}
				if err := s.st.AddBookSeries(r.Context(), d.ID, refs); err != nil {
					s.log.Warn("book series", "book", d.ID, "error", err)
				} else if refs, err := s.st.BookSeries(r.Context(), d.ID); err == nil {
					d.Series = refs
				}
			}
		} else if !errors.Is(err, library.ErrNoFile) {
			s.log.Warn("fb2 meta", "book", d.ID, "error", err)
		}
	} else if !errors.Is(err, errLibraryDirNotConfigured) && !errors.Is(err, store.ErrNotFound) {
		s.log.Warn("book library", "book", d.ID, "error", err)
	}
	seriesList := make([]map[string]any, 0, len(d.Series))
	for _, sr := range d.Series {
		seriesList = append(seriesList, map[string]any{
			"SeriesID": sr.ID, "SeriesTitle": sr.Title, "SeqNumber": sr.SeqNumber,
		})
	}

	resp["series"] = seriesList
	writeJSON(w, resp)
}

// --- Images/* ---

func (s *Server) bookFileOr404(w http.ResponseWriter, r *http.Request) *store.BookFile {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return nil
	}
	f, err := s.st.BookFile(r.Context(), id)
	if err != nil {
		s.apiError(w, err)
		return nil
	}
	return f
}

func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	f := s.bookFileOr404(w, r)
	if f == nil {
		return
	}
	lib, libID, err := s.bookLibraryOwner(r.Context(), f.ID)
	if err != nil {
		s.apiError(w, err)
		return
	}
	gen := s.st.GetMeta(r.Context(), "library."+libID+".cover_gen", "0")
	etag := "\"" + strconv.FormatInt(f.ID, 10) + "-" + gen + "\""
	// Exact match or "*" — weak validators and list forms are not parsed.
	if inm := r.Header.Get("If-None-Match"); inm == etag || inm == "*" {
		w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	data, mime, err := lib.Cover(f.ID, f.Folder, f.File, f.Ext)
	if err != nil {
		s.log.Info("no cover", "book", f.ID, "folder", f.Folder, "file", f.File, "error", err)
		if errors.Is(err, library.ErrNoFile) {
			// Let browsers remember a missing cover instead of
			// re-requesting it on every page view.
			w.Header().Set("Cache-Control", "private, max-age=3600")
		}
		s.apiError(w, err)
		return
	}
	if mime == "" {
		mime = "image/jpeg"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	w.Header().Set("ETag", etag)
	w.Write(data)
}

func (s *Server) handleBookDownload(w http.ResponseWriter, r *http.Request) {
	f := s.bookFileOr404(w, r)
	if f == nil {
		return
	}
	lib := s.bookLibraryOr404(w, r, f.ID)
	if lib == nil {
		return
	}
	rc, size, err := lib.Open(f.Folder, f.File, f.Ext)
	if err != nil {
		s.apiError(w, err)
		return
	}
	defer rc.Close()

	disposition := "attachment"
	if r.URL.Query().Get("inline") == "1" {
		disposition = "inline" // view in browser (PDF etc.)
	}
	w.Header().Set("Content-Type", contentTypeFor(f.Ext))
	w.Header().Set("Content-Disposition", disposition+`; filename="`+f.File+`.`+f.Ext+`"`)
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	// Record the KOReader digest of the served file so positions pushed by
	// e-ink devices (kosync) can be matched back to this book.
	tee := &digestTee{}
	copied, _ := io.Copy(io.MultiWriter(w, tee), rc)
	s.recordDigest(tee, f.ID, size, copied)
}

func (s *Server) handleBookZip(w http.ResponseWriter, r *http.Request) {
	f := s.bookFileOr404(w, r)
	if f == nil {
		return
	}
	lib := s.bookLibraryOr404(w, r, f.ID)
	if lib == nil {
		return
	}
	rc, _, err := lib.Open(f.Folder, f.File, f.Ext)
	if err != nil {
		s.apiError(w, err)
		return
	}
	defer rc.Close()

	// Build the zip in memory so we can set Content-Length: without it OPDS
	// clients (MoonReader) hang "at 100%" waiting for the size. An fb2.zip is
	// small, so buffering is fine.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entry, err := zw.Create(f.File + "." + f.Ext)
	if err == nil {
		_, err = io.Copy(entry, rc)
	}
	if err == nil {
		err = zw.Close()
	}
	if err != nil {
		s.log.Warn("zip stream", "book", f.ID, "error", err)
		s.apiError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+f.File+`.`+f.Ext+`.zip"`)
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.Write(buf.Bytes())
}

func (s *Server) handleBookCompact(w http.ResponseWriter, r *http.Request) {
	f := s.bookFileOr404(w, r)
	if f == nil {
		return
	}
	lib := s.bookLibraryOr404(w, r, f.ID)
	if lib == nil {
		return
	}
	if !strings.EqualFold(f.Ext, "fb2") {
		http.NotFound(w, r)
		return
	}
	rc, _, err := lib.Open(f.Folder, f.File, f.Ext)
	if err != nil {
		s.apiError(w, err)
		return
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		s.apiError(w, err)
		return
	}
	compact := library.StripBinaries(data)
	w.Header().Set("Content-Type", contentTypeFor("fb2"))
	w.Header().Set("Content-Disposition", `attachment; filename="`+f.File+`.compact.fb2"`)
	w.Write(compact)
}

func contentTypeFor(ext string) string {
	switch strings.ToLower(ext) {
	case "fb2":
		return "application/x-fictionbook+xml; charset=utf-8"
	case "epub":
		return "application/epub+zip"
	case "pdf":
		return "application/pdf"
	case "djvu":
		return "image/vnd.djvu"
	case "txt":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
