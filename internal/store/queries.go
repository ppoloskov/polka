package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
)

// Book is a book-list row in the format expected by the frontend.
type Book struct {
	ID          int64
	Title       string
	AuthorNames string
	BookAuthors []BookAuthor
	SeriesTitle string
	SeqNumber   int
	Year        int
	LibRate     float64
	Ext         string
	Size        int64
	Lang        string
	LibraryID   string
	LibraryName string
	Available   bool
}

// BookAuthor is a structured author reference for a book card.
type BookAuthor struct {
	AuthorID int64  `json:"AuthorID"`
	Name     string `json:"Name"`
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
	coalesce((SELECT json_group_array(json_object('AuthorID', author_id, 'Name', name)) FROM (
	          SELECT a.id AS author_id, trim(a.last_name || ' ' || a.first_name || ' ' || a.middle_name) AS name
	          FROM book_authors ba JOIN authors a ON a.id = ba.author_id
	          WHERE ba.book_id = b.id ORDER BY a.id)), '[]'),
`

const bookMetadataColumns = `
	coalesce(b.year, 0),
	b.lib_rate,
	b.ext,
	b.size,
	coalesce(b.lang, '')`

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

// bumpCountsTx shifts the maintained totals inside the caller's transaction:
// the visible book count by the given delta, per-genre counts by code —
// globally and for the library that owns the change (the scoped genre list
// reads per-library counts so the search UI never pays the direct scan).
// Library deltas describe the library's live membership (deleted=0,
// missing=0) and apply regardless of the library being enabled; global
// deltas only count visible books. When the totals are not maintained yet,
// the full computation runs instead — it already sees the caller's change
// in the same transaction. Returns whether the full computation ran.
func (s *Store) bumpCountsTx(
	ctx context.Context,
	tx *sql.Tx,
	visibleDelta int,
	genreDeltas map[string]int,
	libraryID string,
	libraryGenreDeltas map[string]int,
) (bool, error) {
	if !s.metaHoldsCountsTx(ctx, tx) {
		return true, s.maintainCountsTx(ctx, tx)
	}
	if visibleDelta != 0 {
		var value string
		if err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, metaKeysVisibleCount).Scan(&value); err != nil {
			return false, err
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			return false, err
		}
		if err := setMetaTx(ctx, tx, metaKeysVisibleCount, strconv.Itoa(n+visibleDelta)); err != nil {
			return false, err
		}
	}
	if len(genreDeltas) > 0 {
		if err := s.adjustGenreCountsTx(ctx, tx, metaKeysGenreCounts, "", genreDeltas); err != nil {
			return false, err
		}
	}
	if libraryID != "" && (len(libraryGenreDeltas) > 0) {
		// The scoped list key may predate this library's first change;
		// compute it once from the same transaction instead of
		// incrementing an unknown baseline.
		var present string
		if err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, libraryGenreCountsKey(libraryID)).Scan(&present); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return false, err
			}
			if err := s.maintainLibraryGenreCountsTx(ctx, tx, libraryID); err != nil {
				return false, err
			}
		} else if err := s.adjustGenreCountsTx(ctx, tx, libraryGenreCountsKey(libraryID), libraryID, libraryGenreDeltas); err != nil {
			return false, err
		}
	}
	return false, nil
}

// adjustGenreCountsTx applies the deltas to one maintained genre-count JSON
// (key of the global list, or one of a library) inside the transaction.
func (s *Store) adjustGenreCountsTx(ctx context.Context, tx *sql.Tx, key, libraryID string, genreDeltas map[string]int) error {
	var value string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&value); err != nil {
		return err
	}
	var counts []GenreCount
	if json.Unmarshal([]byte(value), &counts) != nil {
		counts = nil
	}
	byCode := make(map[string]int, len(counts))
	for _, g := range counts {
		byCode[g.Code] = g.Books
	}
	for code, delta := range genreDeltas {
		byCode[code] += delta
	}
	merged := make([]GenreCount, 0, len(byCode))
	for code, books := range byCode {
		if books > 0 {
			merged = append(merged, GenreCount{Code: code, Books: books})
		}
	}
	if libraryID != "" {
		// Scoped lists may leave entries with a non-positive count (their
		// semantics differ from the global list, see maintainCountTx).
		b, err := json.Marshal(merged)
		if err != nil {
			return err
		}
		return setMetaTx(ctx, tx, key, string(b))
	}
	// The global list documents only genres with live books.
	if len(merged) == 0 {
		merged = []GenreCount{}
	}
	b, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	return setMetaTx(ctx, tx, key, string(b))
}

// libraryGenreCountsKey is the maintained per-genre count list of one
// library (the scoped genre search reads this instead of scanning).
func libraryGenreCountsKey(libraryID string) string {
	return metaKeysGenreCounts + "." + libraryID
}

// maintainLibraryGenreCountsTx computes and records one library's per-genre
// visible-count list inside the transaction (the scoped genre search uses
// it instead of scanning the join per request).
func (s *Store) maintainLibraryGenreCountsTx(ctx context.Context, tx *sql.Tx, libraryID string) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT g.code, count(*)
		FROM genres g
		JOIN book_genres bg ON bg.genre_id = g.id
		JOIN books b ON b.id = bg.book_id AND b.library_id = ? AND b.deleted = 0 AND b.missing = 0
		GROUP BY g.id
		HAVING count(*) > 0`, libraryID)
	if err != nil {
		return err
	}
	defer rows.Close()
	counts := make([]GenreCount, 0, 384)
	for rows.Next() {
		var g GenreCount
		if err := rows.Scan(&g.Code, &g.Books); err != nil {
			return err
		}
		counts = append(counts, g)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(counts) == 0 {
		counts = []GenreCount{}
	}
	b, err := json.Marshal(counts)
	if err != nil {
		return err
	}
	return setMetaTx(ctx, tx, libraryGenreCountsKey(libraryID), string(b))
}

// bookGenreDeltaCodes returns the genre codes a book belongs to (the deltas
// its visibility change contributes).
func bookGenreDeltaCodes(ctx context.Context, tx *sql.Tx, bookID int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT g.code FROM book_genres bg JOIN genres g ON g.id = bg.genre_id
		WHERE bg.book_id = ?`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var codes []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		codes = append(codes, c)
	}
	return codes, rows.Err()
}

// libraryBookCountTx counts the live (deleted=0, missing=0) books of one
// library: the delta a library enable/disable contributes.
func libraryBookCountTx(ctx context.Context, tx *sql.Tx, libraryID string) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM books b WHERE b.library_id = ? AND b.deleted = 0 AND b.missing = 0`, libraryID).Scan(&n)
	return n, err
}

// libraryGenreDeltaTx returns the per-genre deltas a library's availability
// flip contributes.
func libraryGenreDeltaTx(ctx context.Context, tx *sql.Tx, libraryID string, dir int, out map[string]int) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT g.code, count(*) FROM book_genres bg
		JOIN books b ON b.id = bg.book_id AND b.library_id = ? AND b.deleted = 0 AND b.missing = 0
		JOIN genres g ON g.id = bg.genre_id
		GROUP BY g.id`, libraryID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var n int
		if err := rows.Scan(&code, &n); err != nil {
			return err
		}
		out[code] += dir * n
	}
	return rows.Err()
}

func (s *Store) queryBooks(ctx context.Context, where string, order string, limit, offset int, args ...any) ([]Book, error) {
	// Note: a literal enabled-library IN-list was tried here instead of the
	// availability EXISTS; it regressed the planner into a full
	// re-shuffle (2.2s vs 270ms for a deep "newest" page on the real
	// catalog) and was reverted. The partial live indexes + the bloom-able
	// EXISTS stay; deep-offset cost is bounded by the enabled share of the
	// walked order key.
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
	rows, err := s.readDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var books []Book
	for rows.Next() {
		var b Book
		var authorsJSON string
		if err := rows.Scan(&b.ID, &b.Title, &b.AuthorNames, &authorsJSON, &b.SeriesTitle,
			&b.SeqNumber, &b.Year, &b.LibRate, &b.Ext, &b.Size, &b.Lang,
			&b.LibraryID, &b.LibraryName, &b.Available); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(authorsJSON), &b.BookAuthors); err != nil {
			return nil, fmt.Errorf("decode book authors: %w", err)
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
	if err := s.readDB.QueryRowContext(ctx, `
		SELECT count(*) FROM book_search bs JOIN books b ON b.id = bs.rowid
		WHERE book_search MATCH ? AND `+titleWhere, titleArgs...).Scan(&st.BookTitles); err != nil {
		return st, err
	}
	authorScope, authorScopeArgs := addLibraryScope(`b.deleted = 0 AND `+availableBook, "b", libraryID, nil)
	authorArgs := append([]any{plainQ}, authorScopeArgs...)
	if err := s.readDB.QueryRowContext(ctx, `
		SELECT count(*) FROM authors_search a
		WHERE authors_search MATCH ? AND EXISTS (
			SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id
			WHERE ba.author_id = a.rowid AND `+authorScope+`)`, authorArgs...).Scan(&st.Authors); err != nil {
		return st, err
	}
	seriesScope, seriesScopeArgs := addLibraryScope(`b.deleted = 0 AND `+availableBook, "b", libraryID, nil)
	seriesArgs := append([]any{plainQ}, seriesScopeArgs...)
	if err := s.readDB.QueryRowContext(ctx, `
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
	if err := s.readDB.QueryRowContext(ctx,
		`SELECT count(*) FROM book_search WHERE book_search MATCH ?`, ftsQ).Scan(&matches); err != nil {
		return nil, err
	}
	innerScope := ""
	args := []any{ftsQ, limit, offset}
	if libraryID != "" {
		innerScope = ` AND b.library_id = ?`
		args = []any{ftsQ, libraryID, limit, offset}
	}
	if matches > 5000 {
		return s.scanBooks(ctx, `SELECT`+bookColumns+`
			FROM (SELECT book_search.rowid FROM book_search JOIN books b ON b.id = book_search.rowid
			      WHERE book_search MATCH ? AND b.deleted = 0 AND `+availableBook+innerScope+` LIMIT ? OFFSET ?) m
			JOIN books b ON b.id = m.rowid
			LEFT JOIN series s ON s.id = b.series_id
			JOIN libraries l ON l.id = b.library_id
			ORDER BY b.lib_rate DESC, b.title`, args...)
	}
	return s.scanBooks(ctx, `SELECT`+bookColumns+`
		FROM (SELECT book_search.rowid, rank FROM book_search JOIN books b ON b.id = book_search.rowid
		      WHERE book_search MATCH ? AND b.deleted = 0 AND `+availableBook+innerScope+` ORDER BY rank LIMIT ? OFFSET ?) m
		JOIN books b ON b.id = m.rowid
		LEFT JOIN series s ON s.id = b.series_id
		JOIN libraries l ON l.id = b.library_id
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
	rows, err := s.readDB.QueryContext(ctx, `
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
	rows, err := s.readDB.QueryContext(ctx, `
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

// AuthorBooks returns every book of an author in reading order (OPDS,
// book-form export). AuthorBooksPage paginates the same list for the web
// author page: a prolific author measured whole-page responses of thousands
// of cards.
func (s *Store) AuthorBooks(ctx context.Context, authorID int64, libraryIDs ...string) ([]Book, string, error) {
	return s.authorBooks(ctx, authorID, 0, 0, libraryIDs...)
}

// AuthorBooksPage returns one page of an author's books in reading order;
// limit &lt;= 0 returns the full list.
func (s *Store) AuthorBooksPage(ctx context.Context, authorID int64, limit, offset int, libraryIDs ...string) ([]Book, string, error) {
	return s.authorBooks(ctx, authorID, limit, offset, libraryIDs...)
}

func (s *Store) authorBooks(ctx context.Context, authorID int64, limit, offset int, libraryIDs ...string) ([]Book, string, error) {
	var name string
	err := s.readDB.QueryRowContext(ctx,
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
		`coalesce(s.title, ''), b.series_num, b.title`, limit, offset, args...)
	return books, name, err
}

func (s *Store) SeriesBooks(ctx context.Context, seriesID int64, libraryIDs ...string) ([]Book, string, error) {
	var title string
	err := s.readDB.QueryRowContext(ctx, `SELECT title FROM series WHERE id = ?`, seriesID).Scan(&title)
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
		err := s.readDB.QueryRowContext(ctx,
			`SELECT id, coalesce(nullif(name, ''), code) FROM genres WHERE code = ?`, code).Scan(&genreID, &title)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", ErrNotFound
		}
		if err != nil {
			return nil, "", err
		}
		ids, err := s.genreVisibleIDs(ctx, genreID, libraryID)
		if err != nil {
			return nil, "", err
		}
		// Paginate over the cached visible ids (book order), re-filtering
		// on fetch so a stale list can never surface an unavailable row.
		// Cost is proportional to the page, not the genre size: a
		// 107k-member genre measured 92-180ms per page when every request
		// materialized and re-filtered the whole member list. The offset is
		// user-supplied and the list can shrink between builds, so the
		// window is guarded; the store returns exactly limit rows — the
		// HTTP caller already adds the extra row for its hasMore probe.
		if offset >= len(ids) {
			return nil, title, nil
		}
		window := ids[offset:]
		if len(window) > limit {
			window = window[:limit]
		}
		books, err := s.BooksByIDs(ctx, window, libraryID)
		if err != nil {
			return nil, "", err
		}
		return books, title, nil
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
// The genre set comes from the maintained per-genre counts (recorded with
// the catalog) instead of a per-request scan, and each shelf draws random
// member ids from the cached visible list — preserving the previous SQL
// random-sampling behavior (uniform choice among live books, smaller
// genres contributing whatever they have) while a 1.6M-row catalog
// measured 1.5s-2.8s for the scan-and-sort-everything version (10s+ under
// load).
func (s *Store) CatalogShelves(ctx context.Context, count, limit int, libraryIDs ...string) ([]Shelf, error) {
	libraryID := scopeLibraryID(libraryIDs)
	counts, err := s.CachedGenresWithCounts(ctx, libraryIDs...)
	if err != nil {
		return nil, err
	}
	eligible := make([]GenreCount, 0, len(counts))
	for _, g := range counts {
		if g.Books > 0 {
			eligible = append(eligible, g)
		}
	}
	if len(eligible) == 0 {
		return nil, nil
	}
	rand.Shuffle(len(eligible), func(i, j int) { eligible[i], eligible[j] = eligible[j], eligible[i] })
	if len(eligible) < count {
		count = len(eligible)
	}
	picked := eligible[:count]

	var shelves []Shelf
	for _, g := range picked {
		var genreID int64
		var title string
		if err := s.readDB.QueryRowContext(ctx,
			`SELECT id, coalesce(nullif(name, ''), code) FROM genres WHERE code = ?`, g.Code).Scan(&genreID, &title); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, err
		}
		// A random window of the cached visible member ids: uniform over
		// live books, and page cost is proportional to the window.
		ids, err := s.genreVisibleIDs(ctx, genreID, libraryID)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			continue
		}
		// Random sample without replacement (partial Fisher-Yates); a
		// smaller genre contributes its whole list, so nothing is dropped
		// arbitrarily. The sample is displayed by title, as before.
		window := ids
		if len(ids) > limit {
			window = make([]int64, limit)
			copy(window, ids[:limit])
			for i := limit; i < len(ids); i++ {
				if j := rand.Intn(i + 1); j < limit {
					window[j] = ids[i]
				}
			}
		}
		books, err := s.BooksByIDs(ctx, window, libraryID)
		if err != nil {
			return nil, err
		}
		if len(books) == 0 {
			continue
		}
		// The sampled request order is display order for BooksByIDs; the
		// shelf shows books by title, as before.
		sort.Slice(books, func(i, j int) bool { return books[i].Title < books[j].Title })
		shelves = append(shelves, Shelf{
			ID:      "genre_" + g.Code,
			Title:   title,
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
	rows, err := s.readDB.QueryContext(ctx, `
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

// CachedGenresWithCounts serves the maintained per-genre counts (recorded
// with the catalog, see MaintainCounts); missing values are initialized
// transactionally (EnsureCountsInitialized), never written back from a
// read snapshot that could clobber newer publication totals. Scoped
// queries (a view of one library) stay direct: they are rare, admin-paced.
func (s *Store) CachedGenresWithCounts(ctx context.Context, libraryIDs ...string) ([]GenreCount, error) {
	if libraryID := scopeLibraryID(libraryIDs); libraryID != "" {
		return s.GenresWithCounts(ctx, libraryID)
	}
	if err := s.EnsureCountsInitialized(ctx); err != nil {
		return nil, err
	}
	if v := s.GetMeta(ctx, metaKeysGenreCounts, ""); v != "" {
		var out []GenreCount
		if json.Unmarshal([]byte(v), &out) == nil && out != nil {
			return out, nil
		}
	}
	return s.GenresWithCounts(ctx)
}

// CachedGenresForLibrary serves the maintained per-genre list of one
// library: the library's live membership (deleted=0, missing=0) regardless
// of availability — deleting an invisible book must still update it, and
// re-enabling a library must not resurrect a stale list. Missing lists are
// computed once transactionally (not written back from read snapshots).
func (s *Store) CachedGenresForLibrary(ctx context.Context, libraryID string) ([]GenreCount, error) {
	var exists int
	err := s.readDB.QueryRowContext(ctx, `SELECT count(*) FROM libraries WHERE id = ?`, libraryID).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, nil
	}
	if v := s.GetMeta(ctx, libraryGenreCountsKey(libraryID), ""); v != "" {
		var out []GenreCount
		if json.Unmarshal([]byte(v), &out) == nil && out != nil {
			return out, nil
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := s.maintainLibraryGenreCountsTx(ctx, tx, libraryID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if v := s.GetMeta(ctx, libraryGenreCountsKey(libraryID), ""); v != "" {
		var out []GenreCount
		if json.Unmarshal([]byte(v), &out) == nil {
			return out, nil
		}
	}
	return s.GenresWithCounts(ctx, libraryID)
}

// genreVisibleIDs returns the visible (live and enabled-library) member ids
// of a genre in book order. The list is built once per genre scope and kept
// in memory: pagination over a 107k-member genre re-filtered the whole
// member list on every page, the most expensive request family on the real
// catalog (~120ms per page, serialized further under load). The cache is
// dropped whenever visibility can change (maintainCountsTxs / direct
// mutations via MaintainCounts and publication); a build that overlaps a
// visibility change must not install its pre-change list after the drop, so
// installs are guarded by the generation counter (same pattern as the cover
// cache). Pages re-filter live on fetch: a stale list can shift positions,
// never what a user sees.
func (s *Store) genreVisibleIDs(ctx context.Context, genreID int64, libraryID string) ([]int64, error) {
	key := libraryID + "|" + strconv.FormatInt(genreID, 10)
	s.genreIDs.mu.Lock()
	ids, ok := s.genreIDs.lists[key]
	gen := s.genreIDs.gen
	s.genreIDs.mu.Unlock()
	if ok {
		return ids, nil
	}
	scope := ""
	buildArgs := []any{}
	if libraryID != "" {
		scope = ` AND b.library_id = ?`
		buildArgs = append(buildArgs, libraryID)
	}
	rows, err := s.readDB.QueryContext(ctx, `
		SELECT bg.book_id FROM book_genres bg
		JOIN books b ON b.id = bg.book_id AND b.deleted = 0 AND `+availableBook+scope+`
		WHERE bg.genre_id = ?
		ORDER BY bg.book_id`, append(buildArgs, genreID)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	built := make([]int64, 0, 1024)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		built = append(built, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.genreIDs.mu.Lock()
	defer s.genreIDs.mu.Unlock()
	// The read connection's snapshot may predate a publication that
	// invalidated the cache while this build was running; installing old
	// ids after the drop would keep them until the next invalidation, so
	// only installs that observed the current generation are accepted.
	if s.genreIDs.gen != gen {
		return built, nil
	}
	if s.genreIDs.lists == nil {
		s.genreIDs.lists = map[string][]int64{}
	}
	s.genreIDs.lists[key] = built
	return built, nil
}

// dropGenreVisibleIDs clears the cached per-genre member lists and bumps
// the generation, invalidating builds that started before the change.
func (s *Store) dropGenreVisibleIDs() {
	s.genreIDs.mu.Lock()
	s.genreIDs.lists = nil
	s.genreIDs.gen++
	s.genreIDs.mu.Unlock()
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
	var authorsJSON string
	err := s.readDB.QueryRowContext(ctx,
		`SELECT`+bookColumns+`, b.file, f.name`+bookFrom+
			` JOIN folders f ON f.id = b.folder_id WHERE b.id = ? AND b.deleted = 0 AND `+availableBook, bookID).
		Scan(&d.ID, &d.Title, &d.AuthorNames, &authorsJSON, &d.SeriesTitle, &d.SeqNumber,
			&d.Year, &d.LibRate, &d.Ext, &d.Size, &d.Lang, &d.LibraryID, &d.LibraryName,
			&d.Available, &d.File, &d.Folder)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(authorsJSON), &d.BookAuthors); err != nil {
		return nil, fmt.Errorf("decode book authors: %w", err)
	}

	rows, err := s.readDB.QueryContext(ctx, `
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

	grows, err := s.readDB.QueryContext(ctx, `
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
	rows, err := s.readDB.QueryContext(ctx, `
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
	err := s.readDB.QueryRowContext(ctx, `
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
	rows, err := s.readDB.QueryContext(ctx, `
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
	rows, err := s.readDB.QueryContext(ctx, `
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
