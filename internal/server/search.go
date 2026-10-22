package server

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vestigiumincaligne/polka/internal/genres"
	"github.com/vestigiumincaligne/polka/internal/store"
)

// Search extensions: genres by Russian names and ISBN.

// genreCache — genre counters change rarely, so we cache them.
type genreCache struct {
	mu      sync.Mutex
	counts  []store.GenreCount
	fetched time.Time
}

type genreEntry struct {
	Code  string
	Name  string
	Books int
}

const genreCacheTTL = 5 * time.Minute

func (s *Server) genreList(r *http.Request, libraryIDs ...string) []genreEntry {
	lang := reqLang(r)
	libraryID := ""
	if len(libraryIDs) > 0 {
		libraryID = libraryIDs[0]
	}
	var counts []store.GenreCount
	if libraryID != "" {
		counts, _ = s.st.GenresWithCounts(r.Context(), libraryID)
	} else {
		s.genres.mu.Lock()
		defer s.genres.mu.Unlock()
		if time.Since(s.genres.fetched) >= genreCacheTTL || s.genres.counts == nil {
			if counts, err := s.st.GenresWithCounts(r.Context()); err == nil {
				s.genres.counts = counts
				s.genres.fetched = time.Now()
			}
		}
		counts = s.genres.counts
	}
	// Only counters are cached; names are localized on the fly.
	list := make([]genreEntry, 0, len(counts))
	for _, g := range counts {
		list = append(list, genreEntry{Code: g.Code, Name: genres.NameLang(g.Code, lang), Books: g.Books})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Books > list[j].Books })
	return list
}

// searchGenres filters genres by a substring of the Russian name or the code.
func (s *Server) searchGenres(r *http.Request, query string, libraryIDs ...string) []genreEntry {
	q := strings.ToLower(strings.ReplaceAll(query, "ё", "е"))
	var out []genreEntry
	for _, g := range s.genreList(r, libraryIDs...) {
		name := strings.ToLower(strings.ReplaceAll(g.Name, "ё", "е"))
		if strings.Contains(name, q) || strings.Contains(strings.ToLower(g.Code), q) {
			out = append(out, g)
		}
	}
	return out
}

// GET /main/getBooks/getSearchGenres?search=
func (s *Server) handleGetSearchGenres(w http.ResponseWriter, r *http.Request) {
	query := searchParam(r)
	libraryID := r.URL.Query().Get("libraryId")
	list := make([]map[string]any, 0)
	if query != "" {
		for _, g := range s.searchGenres(r, query, libraryID) {
			list = append(list, map[string]any{
				"GenreCode": g.Code, "GenreName": g.Name, "Books": g.Books,
			})
		}
	}
	writeJSON(w, map[string]any{"genresList": list})
}

// isbnBooks — ISBN search: the local column first, then external title
// resolution followed by a collection search.
func (s *Server) isbnBooks(r *http.Request, query string, libraryIDs ...string) []store.Book {
	libraryID := ""
	if len(libraryIDs) > 0 {
		libraryID = libraryIDs[0]
	}
	books, err := s.st.SearchByISBN(r.Context(), query, libraryID)
	if err == nil && len(books) > 0 {
		return books
	}
	info, err := s.enrich.ResolveISBN(r.Context(), store.NormalizeISBN(query))
	if err != nil || info == nil {
		return nil
	}
	if libraryID == "" {
		if b, err := s.st.MatchBook(r.Context(), info.Title, info.Author); err == nil {
			return []store.Book{*b}
		}
	} else if scoped, err := s.st.SearchTitles(r.Context(), info.Title, 10, libraryID); err == nil && len(scoped) > 0 {
		return scoped
	}
	// Resolution succeeded but there is no exact match — show close title matches.
	if loose, err := s.st.SearchTitles(r.Context(), info.Title, 10, libraryID); err == nil {
		return loose
	}
	return nil
}
