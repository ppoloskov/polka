package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Book is a book-list row in the format expected by the frontend.
type Book struct {
	ID          int64
	Title       string
	AuthorNames string
	SeriesTitle string
	SeqNumber   int
	Year        int
	LibRate     float64
	Ext         string
	Size        int64
	LibraryID   string
	LibraryName string
	Available   bool
}

// BookFile holds the data needed to serve a book file.
type BookFile struct {
	ID     int64
	Title  string
	Folder string
	File   string
	Ext    string
	Size   int64
}

// BookDetails is the full book card.
type BookDetails struct {
	Book
	File    string
	Folder  string
	Authors []AuthorName
	Series  []SeriesRef
	Genres  []string
}

type SeriesRef struct {
	ID        int64
	Title     string
	SeqNumber int
}

type AuthorEntry struct {
	ID    int64
	Name  string
	Books int
}

type SeriesEntry struct {
	ID    int64
	Title string
	Books int
}

var ErrNotFound = errors.New("not found")

const bookIdentityColumns = `
	b.id,
	b.title,
	coalesce((SELECT group_concat(trim(a.last_name || ' ' || a.first_name), ', ')
	          FROM book_authors ba JOIN authors a ON a.id = ba.author_id
	          WHERE ba.book_id = b.id), ''),
`

const bookMetadataColumns = `
	coalesce(b.year, 0),
	b.lib_rate,
	b.ext,
	b.size`

const bookColumns = bookIdentityColumns + `
	coalesce(s.title, ''),
	coalesce(b.series_num, 0),
` + bookMetadataColumns + `,
	b.library_id,
	l.name,
	(b.missing = 0 AND l.enabled = 1)`

const bookFrom = ` FROM books b LEFT JOIN series s ON s.id = b.series_id JOIN libraries l ON l.id = b.library_id`
const availableBook = `b.missing = 0 AND EXISTS (SELECT 1 FROM libraries l WHERE l.id = b.library_id AND l.enabled = 1)`

func scopeLibraryID(ids []string) string {
	if len(ids) > 0 {
		return ids[0]
	}
	return ""
}

func addLibraryScope(where, alias, libraryID string, args []any) (string, []any) {
	if libraryID == "" {
		return where, args
	}
	if where != "" {
		where += ` AND `
	}
	return where + alias + `.library_id = ?`, append(args, libraryID)
}

func (s *Store) queryBooks(ctx context.Context, where string, order string, limit, offset int, args ...any) ([]Book, error) {
	q := `SELECT` + bookColumns + bookFrom + ` WHERE b.deleted = 0 AND ` + availableBook
	if where != "" {
		q += ` AND ` + where
	}
	if order != "" {
		q += ` ORDER BY ` + order
	}
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d OFFSET %d`, limit, offset)
	}
	return s.scanBooks(ctx, q, args...)
}

// scanBooks runs a query that selects bookColumns and scans the rows.
func (s *Store) scanBooks(ctx context.Context, q string, args ...any) ([]Book, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var books []Book
	for rows.Next() {
		var b Book
		if err := rows.Scan(&b.ID, &b.Title, &b.AuthorNames, &b.SeriesTitle,
			&b.SeqNumber, &b.Year, &b.LibRate, &b.Ext, &b.Size,
			&b.LibraryID, &b.LibraryName, &b.Available); err != nil {
			return nil, err
		}
		books = append(books, b)
	}
	return books, rows.Err()
}

// ftsQuery turns user input into a safe prefix FTS5 expression:
// each token becomes "token"* , terms are joined with AND.
func ftsQuery(input, column string) string {
	var terms []string
	for _, tok := range strings.FieldsFunc(input, func(r rune) bool {
		return !isWordRune(r)
	}) {
		tok = strings.ReplaceAll(tok, `"`, "")
		if tok == "" {
			continue
		}
		term := `"` + tok + `"*`
		if column != "" {
			term = column + `:` + term
		}
		// unicode61 does not equate ё and е: "Вишнёвый" would not find
		// "Вишневый", so we search for both forms.
		if alt := strings.NewReplacer("ё", "е", "Ё", "Е").Replace(tok); alt != tok {
			altTerm := `"` + alt + `"*`
			if column != "" {
				altTerm = column + `:` + altTerm
			}
			term = `(` + term + ` OR ` + altTerm + `)`
		}
		terms = append(terms, term)
	}
	// Explicit AND: FTS5 does not accept implicit joining after a parenthesized group.
	return strings.Join(terms, " AND ")
}

func isWordRune(r rune) bool {
	return r == '-' || r == '_' ||
		(r >= '0' && r <= '9') ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		r > 127
}

// --- Search ---

type SearchStats struct {
	BookTitles, Authors, BookSeries int
}

func (s *Store) SearchStats(ctx context.Context, query string, libraryIDs ...string) (SearchStats, error) {
	var st SearchStats
	titleQ, plainQ := ftsQuery(query, "title"), ftsQuery(query, "")
	libraryID := scopeLibraryID(libraryIDs)
	if plainQ == "" {
		return st, nil
	}
	titleWhere, titleArgs := addLibraryScope(`b.deleted = 0 AND `+availableBook, "b", libraryID, []any{titleQ})
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM book_search bs JOIN books b ON b.id = bs.rowid
		WHERE book_search MATCH ? AND `+titleWhere, titleArgs...).Scan(&st.BookTitles); err != nil {
		return st, err
	}
	authorScope, authorScopeArgs := addLibraryScope(`b.deleted = 0 AND `+availableBook, "b", libraryID, nil)
	authorArgs := append([]any{plainQ}, authorScopeArgs...)
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM authors_search a
		WHERE authors_search MATCH ? AND EXISTS (
			SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id
			WHERE ba.author_id = a.rowid AND `+authorScope+`)`, authorArgs...).Scan(&st.Authors); err != nil {
		return st, err
	}
	seriesScope, seriesScopeArgs := addLibraryScope(`b.deleted = 0 AND `+availableBook, "b", libraryID, nil)
	seriesArgs := append([]any{plainQ}, seriesScopeArgs...)
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM series_search ss
		WHERE series_search MATCH ? AND EXISTS (
			SELECT 1 FROM book_series bs JOIN books b ON b.id = bs.book_id
			WHERE bs.series_id = ss.rowid AND `+seriesScope+`)`, seriesArgs...).Scan(&st.BookSeries); err != nil {
		return st, err
	}
	return st, nil
}

func (s *Store) SearchTitles(ctx context.Context, query string, limit int, libraryIDs ...string) ([]Book, error) {
	q := ftsQuery(query, "title")
	if q == "" {
		return nil, nil
	}
	return s.rankedSearch(ctx, q, limit, 0, scopeLibraryID(libraryIDs))
}

// rankedSearch returns FTS matches ordered by relevance (bm25), with the
// library rating breaking ties — reprints and same-titled editions with a
// higher rating come first. When the query matches a huge share of the
// library, ranking hundreds of thousands of rows costs more than it is
// worth: the guard keeps the old take-first behavior, sorted by rating.
func (s *Store) rankedSearch(ctx context.Context, ftsQ string, limit, offset int, libraryID string) ([]Book, error) {
	var matches int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM book_search WHERE book_search MATCH ?`, ftsQ).Scan(&matches); err != nil {
		return nil, err
	}
	innerScope, outerScope := "", ""
	args := []any{ftsQ, limit, offset}
	if libraryID != "" {
		innerScope, outerScope = ` AND b.library_id = ?`, ` AND b.library_id = ?`
		args = []any{ftsQ, libraryID, limit, offset, libraryID}
	}
	if matches > 5000 {
		args = []any{ftsQ, limit, offset}
		if libraryID != "" {
			args = []any{ftsQ, libraryID, limit, offset}
		}
		return s.scanBooks(ctx, `SELECT`+bookColumns+`
			FROM (SELECT book_search.rowid FROM book_search JOIN books b ON b.id = book_search.rowid
			      WHERE book_search MATCH ? AND b.deleted = 0 AND `+availableBook+innerScope+` LIMIT ? OFFSET ?) m
			JOIN books b ON b.id = m.rowid
			LEFT JOIN series s ON s.id = b.series_id
			JOIN libraries l ON l.id = b.library_id
			WHERE b.deleted = 0 AND `+availableBook+`
			ORDER BY b.lib_rate DESC, b.title`, args...)
	}
	return s.scanBooks(ctx, `SELECT`+bookColumns+`
		FROM (SELECT book_search.rowid, rank FROM book_search JOIN books b ON b.id = book_search.rowid
		      WHERE book_search MATCH ? AND b.deleted = 0 AND `+availableBook+innerScope+` ORDER BY rank LIMIT ? OFFSET ?) m
		JOIN books b ON b.id = m.rowid
		LEFT JOIN series s ON s.id = b.series_id
		JOIN libraries l ON l.id = b.library_id
		WHERE b.deleted = 0 AND `+availableBook+outerScope+`
		ORDER BY m.rank, b.lib_rate DESC, b.id`, args...)
}

func (s *Store) SearchAuthors(ctx context.Context, query string, limit int, libraryIDs ...string) ([]AuthorEntry, error) {
	q := ftsQuery(query, "")
	if q == "" {
		return nil, nil
	}
	libraryID := scopeLibraryID(libraryIDs)
	countWhere, countArgs := addLibraryScope(`b.deleted = 0 AND `+availableBook, "b", libraryID, nil)
	args := append([]any{}, countArgs...)
	args = append(args, q)
	whereScope := ""
	if libraryID != "" {
		whereScope = ` AND EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id
			WHERE ba.author_id = a.id AND b.deleted = 0 AND ` + countWhere + `)`
		args = append(args, libraryID)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id,
		       trim(a.last_name || ' ' || a.first_name || ' ' || a.middle_name),
		       (SELECT count(*) FROM book_authors ba JOIN books b ON b.id = ba.book_id
		        WHERE ba.author_id = a.id AND `+countWhere+`)
		FROM authors a
		WHERE a.id IN (SELECT rowid FROM authors_search WHERE authors_search MATCH ?)
		`+whereScope+`
		ORDER BY a.last_name, a.first_name
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AuthorEntry
	for rows.Next() {
		var e AuthorEntry
		if err := rows.Scan(&e.ID, &e.Name, &e.Books); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) SearchSeries(ctx context.Context, query string, limit int, libraryIDs ...string) ([]SeriesEntry, error) {
	q := ftsQuery(query, "")
	if q == "" {
		return nil, nil
	}
	libraryID := scopeLibraryID(libraryIDs)
	countWhere, countArgs := addLibraryScope(`b.deleted = 0 AND `+availableBook, "b", libraryID, nil)
	args := append([]any{}, countArgs...)
	args = append(args, q)
	whereScope := ""
	if libraryID != "" {
		whereScope = ` AND EXISTS (SELECT 1 FROM book_series bs JOIN books b ON b.id = bs.book_id
			WHERE bs.series_id = s.id AND b.deleted = 0 AND ` + countWhere + `)`
		args = append(args, libraryID)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.title,
		       (SELECT count(*) FROM book_series bs JOIN books b ON b.id = bs.book_id
		         WHERE bs.series_id = s.id AND `+countWhere+`)
		FROM series s
		WHERE s.id IN (SELECT rowid FROM series_search WHERE series_search MATCH ?)
		`+whereScope+`
		ORDER BY s.title
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SeriesEntry
	for rows.Next() {
		var e SeriesEntry
		if err := rows.Scan(&e.ID, &e.Title, &e.Books); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) AuthorBooks(ctx context.Context, authorID int64, libraryIDs ...string) ([]Book, string, error) {
	var name string
	err := s.db.QueryRowContext(ctx,
		`SELECT trim(last_name || ' ' || first_name || ' ' || middle_name) FROM authors WHERE id = ?`,
		authorID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	where, args := addLibraryScope(`b.id IN (SELECT book_id FROM book_authors WHERE author_id = ?)`, "b", scopeLibraryID(libraryIDs), []any{authorID})
	books, err := s.queryBooks(ctx, where,
		`coalesce(s.title, ''), b.series_num, b.title`, 0, 0, args...)
	return books, name, err
}

func (s *Store) SeriesBooks(ctx context.Context, seriesID int64, libraryIDs ...string) ([]Book, string, error) {
	var title string
	err := s.db.QueryRowContext(ctx, `SELECT title FROM series WHERE id = ?`, seriesID).Scan(&title)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	where, args := addLibraryScope(`bs.series_id = ? AND b.deleted = 0 AND `+availableBook, "b", scopeLibraryID(libraryIDs), []any{seriesID})
	books, err := s.scanBooks(ctx, `SELECT`+bookIdentityColumns+`s.title, bs.series_num,`+bookMetadataColumns+`,
		b.library_id, l.name, (b.missing = 0 AND l.enabled = 1)
        FROM book_series bs JOIN books b ON b.id = bs.book_id JOIN series s ON s.id = bs.series_id JOIN libraries l ON l.id=b.library_id
	        WHERE `+where+` ORDER BY bs.series_num, b.title`, args...)
	return books, title, err
}

// --- Shelves ---

type Shelf struct {
	ID      string
	Title   string
	Books   []Book
	HasMore bool
}

// shelfWhere returns the condition and ordering for built-in shelves.
func shelfWhere(shelfID string) (where, order string, ok bool) {
	switch shelfID {
	case "newest":
		return ``, `b.added DESC, b.id DESC`, true
	case "top_rated":
		return `b.lib_rate >= 4`, `b.lib_rate DESC, b.id DESC`, true
	case "well_curated":
		return `b.lib_rate BETWEEN 1 AND 3`, `b.lib_rate DESC, b.id DESC`, true
	}
	return "", "", false
}

func (s *Store) HomeShelves(ctx context.Context, limit int, libraryIDs ...string) ([]Shelf, error) {
	var shelves []Shelf
	libraryID := scopeLibraryID(libraryIDs)
	for _, id := range []string{"newest", "top_rated", "well_curated"} {
		books, _, err := s.ShelfBooks(ctx, id, limit, 0, libraryID)
		if err != nil {
			return nil, err
		}
		shelves = append(shelves, Shelf{ID: id, Books: books, HasMore: len(books) == limit})
	}
	return shelves, nil
}

// ShelfBooks returns a page of a built-in ("newest"...) or genre
// ("genre_<code>") shelf.
func (s *Store) ShelfBooks(ctx context.Context, shelfID string, limit, offset int, libraryIDs ...string) ([]Book, string, error) {
	libraryID := scopeLibraryID(libraryIDs)
	if code, isGenre := strings.CutPrefix(shelfID, "genre_"); isGenre {
		var genreID int64
		var title string
		err := s.db.QueryRowContext(ctx,
			`SELECT id, coalesce(nullif(name, ''), code) FROM genres WHERE code = ?`, code).Scan(&genreID, &title)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", ErrNotFound
		}
		if err != nil {
			return nil, "", err
		}
		where, args := addLibraryScope(`b.id IN (SELECT book_id FROM book_genres WHERE genre_id = ?)`, "b", libraryID, []any{genreID})
		books, err := s.queryBooks(ctx, where, `b.id`, limit, offset, args...)
		return books, title, err
	}

	where, order, ok := shelfWhere(shelfID)
	if !ok {
		return nil, "", ErrNotFound
	}
	where, args := addLibraryScope(where, "b", libraryID, nil)
	books, err := s.queryBooks(ctx, where, order, limit, offset, args...)
	return books, "", err
}

// CatalogShelves returns random genre selections for the catalog page.
func (s *Store) CatalogShelves(ctx context.Context, count, limit int, libraryIDs ...string) ([]Shelf, error) {
	libraryID := scopeLibraryID(libraryIDs)
	genreScope := ""
	genreArgs := []any{}
	if libraryID != "" {
		genreScope = ` AND b.library_id = ?`
		genreArgs = append(genreArgs, libraryID)
	}
	genreArgs = append(genreArgs, count)
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.id, g.code, coalesce(nullif(g.name, ''), g.code)
		FROM genres g
		WHERE EXISTS (SELECT 1 FROM book_genres bg JOIN books b ON b.id = bg.book_id
		              WHERE bg.genre_id = g.id AND b.deleted = 0 AND `+availableBook+genreScope+`)
		ORDER BY random()
		LIMIT ?`, genreArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type genre struct {
		id    int64
		code  string
		title string
	}
	var picked []genre
	for rows.Next() {
		var g genre
		if err := rows.Scan(&g.id, &g.code, &g.title); err != nil {
			return nil, err
		}
		picked = append(picked, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var shelves []Shelf
	for _, g := range picked {
		where := `b.id IN (SELECT bg.book_id FROM book_genres bg JOIN books lb ON lb.id = bg.book_id AND lb.deleted = 0
			          AND lb.missing = 0 AND EXISTS (SELECT 1 FROM libraries l WHERE l.id = lb.library_id AND l.enabled = 1)
		          WHERE bg.genre_id = ?`
		args := []any{g.id}
		if libraryID != "" {
			where += ` AND lb.library_id = ?`
			args = append(args, libraryID)
		}
		where += ` ORDER BY random() LIMIT ?)`
		args = append(args, limit)
		where, args = addLibraryScope(where, "b", libraryID, args)
		books, err := s.queryBooks(ctx, where, `b.title`, 0, 0, args...)
		if err != nil {
			return nil, err
		}
		if len(books) == 0 {
			continue
		}
		shelves = append(shelves, Shelf{
			ID:      "genre_" + g.code,
			Title:   g.title,
			Books:   books,
			HasMore: len(books) == limit,
		})
	}
	return shelves, nil
}

// --- OPDS ---

type GenreCount struct {
	Code  string
	Books int
}

// GenresWithCounts returns genres that have live (non-deleted) books.
func (s *Store) GenresWithCounts(ctx context.Context, libraryIDs ...string) ([]GenreCount, error) {
	libraryID := scopeLibraryID(libraryIDs)
	where, args := addLibraryScope(`b.deleted = 0 AND `+availableBook, "b", libraryID, nil)
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.code, count(*)
		FROM genres g
		JOIN book_genres bg ON bg.genre_id = g.id
		JOIN books b ON b.id = bg.book_id AND `+where+`
		GROUP BY g.id
		HAVING count(*) > 0`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GenreCount
	for rows.Next() {
		var g GenreCount
		if err := rows.Scan(&g.Code, &g.Books); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SearchBooks searches across all fields (title, authors, series)
// with pagination, for the OPDS catalog.
func (s *Store) SearchBooks(ctx context.Context, query string, limit, offset int) ([]Book, error) {
	q := ftsQuery(query, "")
	if q == "" {
		return nil, nil
	}
	return s.rankedSearch(ctx, q, limit, offset, "")
}

// --- Book card and book file ---

func (s *Store) BookDetails(ctx context.Context, bookID int64) (*BookDetails, error) {
	d := &BookDetails{}
	err := s.db.QueryRowContext(ctx,
		`SELECT`+bookColumns+`, b.file, f.name`+bookFrom+
			` JOIN folders f ON f.id = b.folder_id WHERE b.id = ? AND b.deleted = 0 AND `+availableBook, bookID).
		Scan(&d.ID, &d.Title, &d.AuthorNames, &d.SeriesTitle, &d.SeqNumber,
			&d.Year, &d.LibRate, &d.Ext, &d.Size, &d.LibraryID, &d.LibraryName,
			&d.Available, &d.File, &d.Folder)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.last_name, a.first_name, a.middle_name
		FROM book_authors ba JOIN authors a ON a.id = ba.author_id
		WHERE ba.book_id = ? ORDER BY a.last_name`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a AuthorName
		if err := rows.Scan(&a.ID, &a.Last, &a.First, &a.Middle); err != nil {
			return nil, err
		}
		d.Authors = append(d.Authors, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	d.Series, err = s.BookSeries(ctx, bookID)
	if err != nil {
		return nil, err
	}

	grows, err := s.db.QueryContext(ctx, `
		SELECT g.code FROM book_genres bg JOIN genres g ON g.id = bg.genre_id
		WHERE bg.book_id = ?`, bookID)
	if err != nil {
		return nil, err
	}
	defer grows.Close()
	for grows.Next() {
		var code string
		if err := grows.Scan(&code); err != nil {
			return nil, err
		}
		d.Genres = append(d.Genres, code)
	}
	return d, grows.Err()
}

// SampleFolders returns up to limit catalog folder (archive) names, to check
// after an import that the archives actually exist on disk.
func (s *Store) SampleFolders(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT name FROM folders
		WHERE id IN (SELECT folder_id FROM books b WHERE deleted = 0 AND `+availableBook+`)
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) BookFile(ctx context.Context, bookID int64) (*BookFile, error) {
	f := &BookFile{}
	err := s.db.QueryRowContext(ctx, `
		SELECT b.id, b.title, fo.name, b.file, b.ext, b.size
		FROM books b JOIN folders fo ON fo.id = b.folder_id
		WHERE b.id = ? AND b.deleted = 0 AND `+availableBook, bookID).
		Scan(&f.ID, &f.Title, &f.Folder, &f.File, &f.Ext, &f.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return f, err
}

// AuthorsByPrefix lists authors whose last name falls in [lo, hi),
// ordered by name — for alphabetical OPDS browsing. Uses idx_authors_name.
func (s *Store) AuthorsByPrefix(ctx context.Context, lo, hi string, limit, offset int) ([]AuthorEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id,
		       trim(a.last_name || ' ' || a.first_name || ' ' || a.middle_name),
		       (SELECT count(*) FROM book_authors ba JOIN books b ON b.id = ba.book_id
		        WHERE ba.author_id = a.id AND b.deleted = 0 AND `+availableBook+`)
		FROM authors a
		WHERE a.last_name >= ? AND a.last_name < ?
		  AND EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id
		              WHERE ba.author_id = a.id AND b.deleted = 0 AND `+availableBook+`)
		ORDER BY a.last_name, a.first_name
		LIMIT ? OFFSET ?`, lo, hi, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuthorEntry
	for rows.Next() {
		var e AuthorEntry
		if err := rows.Scan(&e.ID, &e.Name, &e.Books); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SeriesByPrefix lists series whose title falls in [lo, hi), ordered by title.
func (s *Store) SeriesByPrefix(ctx context.Context, lo, hi string, limit, offset int) ([]SeriesEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.title,
		       (SELECT count(*) FROM book_series bs JOIN books b ON b.id = bs.book_id
		        WHERE bs.series_id = s.id AND b.deleted = 0 AND `+availableBook+`)
		FROM series s
		WHERE s.title >= ? AND s.title < ?
		  AND EXISTS (SELECT 1 FROM book_series bs JOIN books b ON b.id = bs.book_id
		              WHERE bs.series_id = s.id AND b.deleted = 0 AND `+availableBook+`)
		ORDER BY s.title
		LIMIT ? OFFSET ?`, lo, hi, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SeriesEntry
	for rows.Next() {
		var e SeriesEntry
		if err := rows.Scan(&e.ID, &e.Title, &e.Books); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
