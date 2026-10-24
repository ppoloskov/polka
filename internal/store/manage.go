package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// lookupOrInsert returns the row id for a unique value, creating the row
// if needed. afterInsert is called for newly created rows (FTS etc.).
func lookupOrInsert(ctx context.Context, tx *sql.Tx, selectQ, insertQ string, afterInsert func(id int64) error, args ...any) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, selectQ, args...).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, insertQ, args...)
	if err != nil {
		return 0, err
	}
	if id, err = res.LastInsertId(); err != nil {
		return 0, err
	}
	if afterInsert != nil {
		if err := afterInsert(id); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// AddBook adds a single book (web upload) with all its relations and
// updates the search indexes. The maintained totals ride the same
// transaction: a failed count adjustment rolls the book back with the
// upload, so the success/failure contract of the response is preserved
// (the upload cleanup deletes the file on error; a committed row pointing
// at a deleted file would be the worst case).
func (s *Store) AddBook(ctx context.Context, b *BookInput) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := addBookTx(ctx, tx, b)
	if err != nil {
		return 0, err
	}
	// The effective library after addBookTx normalization (a missing
	// LibraryID defaults to the legacy library).
	var effectiveLibrary string
	if err := tx.QueryRowContext(ctx, `SELECT library_id FROM books WHERE id = ?`, id).Scan(&effectiveLibrary); err != nil {
		return 0, err
	}
	// The book is inserted live: the library composition always gains it,
	// while the global visible totals only when the library is enabled
	// (invisible books never belong to genre totals).
	visibleDelta := 0
	globalGenreDeltas := map[string]int{}
	libraryGenreDeltas := map[string]int{}
	for _, code := range b.Genres {
		libraryGenreDeltas[code] = 1 // duplicates map to one membership
	}
	if enabled, err := libraryEnabledTx(ctx, tx, effectiveLibrary); err != nil {
		return 0, err
	} else if enabled {
		visibleDelta = 1
		for _, code := range b.Genres {
			globalGenreDeltas[code] = 1 // duplicates map to one membership
		}
	}
	if _, err := s.bumpCountsTx(ctx, tx, visibleDelta, globalGenreDeltas, effectiveLibrary, libraryGenreDeltas); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	// The new book joins the cached genre member lists.
	s.dropGenreVisibleIDs()
	return id, nil
}

func libraryEnabledTx(ctx context.Context, tx *sql.Tx, libraryID string) (bool, error) {
	var enabled int
	err := tx.QueryRowContext(ctx, `SELECT enabled FROM libraries WHERE id = ?`, libraryID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && enabled == 1, err
}

func addBookTx(ctx context.Context, tx *sql.Tx, b *BookInput) (int64, error) {
	bookID, err := allocateBookID(ctx, tx)
	if err != nil {
		return 0, err
	}
	folderID, err := lookupOrInsert(ctx, tx,
		`SELECT id FROM folders WHERE name = ?`, `INSERT INTO folders (name) VALUES (?)`, nil, b.Folder)
	if err != nil {
		return 0, err
	}

	series := b.seriesRefs()
	primary := SeriesRef{}
	if len(series) > 0 {
		primary = series[0]
	}
	var seriesID any
	if primary.Title != "" {
		id, err := lookupOrInsert(ctx, tx,
			`SELECT id FROM series WHERE title = ?`, `INSERT INTO series (title) VALUES (?)`,
			func(id int64) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO series_search (rowid, title) VALUES (?, ?)`, id, primary.Title)
				return err
			}, primary.Title)
		if err != nil {
			return 0, err
		}
		seriesID = id
	}
	var seriesNum any
	if primary.SeqNumber > 0 {
		seriesNum = primary.SeqNumber
	}
	var year any
	if b.Year > 0 {
		year = b.Year
	}
	libraryID := b.LibraryID
	if libraryID == "" {
		libraryID = "legacy"
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO books (id, lib_id, title, series_id, series_num, folder_id, file, ext, size, lang, year, added, lib_rate, deleted, file_hash, content_hash, isbn, library_id, source_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?)`,
		bookID, b.LibID, b.Title, seriesID, seriesNum, folderID, b.File, b.Ext, b.Size, b.Lang, year, b.Added, b.Rate, b.FileHash, b.ContentHash, NormalizeISBN(b.ISBN), libraryID, b.SourceKey)
	if err != nil {
		return 0, err
	}
	if _, err := addBookSeries(ctx, tx, bookID, series); err != nil {
		return 0, err
	}
	var authorNames []string
	for _, a := range b.Authors {
		name := strings.TrimSpace(a.Last + " " + a.First + " " + a.Middle)
		authorNames = append(authorNames, name)
		id, err := lookupOrInsert(ctx, tx,
			`SELECT id FROM authors WHERE last_name = ? AND first_name = ? AND middle_name = ?`,
			`INSERT INTO authors (last_name, first_name, middle_name) VALUES (?, ?, ?)`,
			func(id int64) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO authors_search (rowid, name) VALUES (?, ?)`, id, name)
				return err
			}, a.Last, a.First, a.Middle)
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO book_authors (book_id, author_id) VALUES (?, ?)`, bookID, id); err != nil {
			return 0, err
		}
	}
	for _, g := range b.Genres {
		id, err := lookupOrInsert(ctx, tx,
			`SELECT id FROM genres WHERE code = ?`, `INSERT INTO genres (code) VALUES (?)`, nil, g)
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO book_genres (book_id, genre_id) VALUES (?, ?)`, bookID, id); err != nil {
			return 0, err
		}
	}
	for _, k := range b.Keywords {
		id, err := lookupOrInsert(ctx, tx,
			`SELECT id FROM keywords WHERE name = ?`, `INSERT INTO keywords (name) VALUES (?)`, nil, k)
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO book_keywords (book_id, keyword_id) VALUES (?, ?)`, bookID, id); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO book_search (rowid, title, authors, series) VALUES (?, ?, ?, ?)`,
		bookID, b.Title, strings.Join(authorNames, " "), seriesTitles(series)); err != nil {
		return 0, err
	}
	return bookID, nil
}

// SetBookDeleted hides or restores a book, keeping the FTS index in sync
// and the maintained totals in the same transaction, so a failed count
// adjustment fails the whole mutation instead of leaving durable counts
// behind.
func (s *Store) SetBookDeleted(ctx context.Context, bookID int64, deleted bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var wasDeleted, missing, libraryExists int
	var libraryID string
	err = tx.QueryRowContext(ctx, `
		SELECT b.deleted, b.missing,
		       coalesce((SELECT 1 FROM libraries l WHERE l.id = b.library_id), 0), b.library_id
		FROM books b WHERE b.id = ?`, bookID).Scan(&wasDeleted, &missing, &libraryExists, &libraryID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE books SET deleted = ? WHERE id = ?`, boolToInt(deleted), bookID); err != nil {
		return err
	}

	// The visibility delta only applies when the library is enabled and the
	// book is not swept to missing.
	enabled := false
	if libraryExists == 1 {
		if enabled, err = libraryEnabledTx(ctx, tx, libraryID); err != nil {
			return err
		}
	}
	// Two independent change sets: the global visible totals (only when the
	// library is enabled and the visible state flipped), and the library's
	// own live composition (which changes even in a disabled library).
	wasVisible := wasDeleted == 0 && missing == 0 && enabled
	isVisible := !deleted && missing == 0 && enabled
	wasLive := wasDeleted == 0 && missing == 0
	isLive := !deleted && missing == 0
	var genreDeltas map[string]int
	var libraryGenreDeltas map[string]int
	visibleDelta := 0
	if wasVisible != isVisible {
		visibleDelta = -1
		if isVisible {
			visibleDelta = 1
		}
	}
	if wasLive != isLive {
		liveDelta := -1
		if isLive {
			liveDelta = 1
		}
		if codes, err := bookGenreDeltaCodes(ctx, tx, bookID); err != nil {
			return err
		} else {
			// One adjustment per genre code, accumulated across the book's
			// whole genre set (recreating the map per code would keep only
			// the last genre's delta).
			libraryGenreDeltas = map[string]int{}
			if wasVisible != isVisible {
				genreDeltas = map[string]int{}
			}
			for _, code := range codes {
				libraryGenreDeltas[code] += liveDelta
				if wasVisible != isVisible {
					genreDeltas[code] += visibleDelta
				}
			}
		}
	}
	if visibleDelta != 0 || len(genreDeltas) > 0 || len(libraryGenreDeltas) > 0 || !s.metaHoldsCountsTx(ctx, tx) {
		if _, err := s.bumpCountsTx(ctx, tx, visibleDelta, genreDeltas, libraryID, libraryGenreDeltas); err != nil {
			return err
		}
	}

	// Deleted books are kept out of the FTS index (so search counts stay
	// honest); drop any stale row first so a restore never hits a
	// duplicate rowid.
	if _, err = tx.ExecContext(ctx, `DELETE FROM book_search WHERE rowid = ?`, bookID); err != nil {
		return err
	}
	if !deleted {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO book_search (rowid, title, authors, series)
			SELECT b.id, b.title,
			       coalesce((SELECT group_concat(trim(a.last_name || ' ' || a.first_name), ', ')
			                 FROM book_authors ba JOIN authors a ON a.id = ba.author_id
			                 WHERE ba.book_id = b.id), ''),
			       coalesce((SELECT group_concat(s.title, ' ') FROM book_series bs JOIN series s ON s.id = bs.series_id
                          WHERE bs.book_id = b.id), '')
			FROM books b JOIN libraries l ON l.id = b.library_id
			WHERE b.id = ? AND b.missing = 0 AND l.enabled = 1`, bookID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Visibility changed: the cached genre member lists are stale.
	if wasVisible != isVisible {
		s.dropGenreVisibleIDs()
	}
	return nil
}

// Clear empties the collection (for re-import on a running server).
// The user database and meta are not touched. The deletes and the
// maintained totals reset ride the same transaction so the counters can
// never commit ahead of the emptied catalog.
func (s *Store) Clear(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tables := []string{
		"book_search", "authors_search", "series_search",
		"book_authors", "book_genres", "book_keywords", "book_series",
		"books", "authors", "series", "genres", "keywords", "folders", "book_digests",
	}
	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	if err := setMetaTx(ctx, tx, metaKeysVisibleCount, "0"); err != nil {
		return err
	}
	if err := setMetaTx(ctx, tx, metaKeysGenreCounts, "[]"); err != nil {
		return err
	}
	// Every library's scoped genre list resets with the catalog: stale
	// per-library totals would survive a Clear otherwise.
	libRows, err := tx.QueryContext(ctx, `SELECT id FROM libraries`)
	if err != nil {
		return err
	}
	var libraryIDs []string
	for libRows.Next() {
		var id string
		if err := libRows.Scan(&id); err != nil {
			libRows.Close()
			return err
		}
		libraryIDs = append(libraryIDs, id)
	}
	libRows.Close()
	if err := libRows.Err(); err != nil {
		return err
	}
	for _, id := range libraryIDs {
		if err := setMetaTx(ctx, tx, libraryGenreCountsKey(id), "[]"); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.dropGenreVisibleIDs()
	return nil
}

// FindByHash looks up a live book by an exact match of the file hash or
// the normalized-text hash.
func (s *Store) FindByHash(ctx context.Context, column, hash string, libraryIDs ...string) (*Book, error) {
	if hash == "" {
		return nil, ErrNotFound
	}
	if column != "file_hash" && column != "content_hash" {
		return nil, fmt.Errorf("bad hash column %q", column)
	}
	where, args := addLibraryScope(`b.`+column+` = ?`, "b", scopeLibraryID(libraryIDs), []any{hash})
	books, err := s.queryBooks(ctx, where, "", 1, 0, args...)
	if err != nil {
		return nil, err
	}
	if len(books) == 0 {
		return nil, ErrNotFound
	}
	return &books[0], nil
}

// SetBookDigest remembers a KOReader-style digest of a served book file.
func (s *Store) SetBookDigest(ctx context.Context, digest string, bookID int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO book_digests (digest, book_id) VALUES (?, ?)
		ON CONFLICT (digest) DO UPDATE SET book_id = excluded.book_id`, digest, bookID)
	return err
}

// BookIDByDigest maps a KOReader document digest back to a book.
func (s *Store) BookIDByDigest(ctx context.Context, digest string) (int64, error) {
	var id int64
	err := s.readDB.QueryRowContext(ctx, `SELECT book_id FROM book_digests WHERE digest = ?`, digest).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// normalizeForMatch normalizes a string for metadata comparison:
// letters and digits only, lowercase, Cyrillic yo collapsed to ye.
func normalizeForMatch(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r == 'ё' {
			r = 'е'
		}
		if isWordRune(r) && r != '-' && r != '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// FindSimilar finds books with the same normalized title and at least
// one shared author (by last name) — candidates for re-edition duplicates.
func (s *Store) FindSimilar(ctx context.Context, title string, authorLastNames []string, libraryIDs ...string) ([]Book, error) {
	q := ftsQuery(title, "title")
	if q == "" {
		return nil, nil
	}
	where, args := addLibraryScope(`b.id IN (SELECT rowid FROM book_search WHERE book_search MATCH ?)`, "b", scopeLibraryID(libraryIDs), []any{q})
	candidates, err := s.queryBooks(ctx, where, "", 50, 0, args...)
	if err != nil {
		return nil, err
	}

	wantTitle := normalizeForMatch(title)
	wantAuthors := make(map[string]bool, len(authorLastNames))
	for _, a := range authorLastNames {
		if n := normalizeForMatch(a); n != "" {
			wantAuthors[n] = true
		}
	}

	var similar []Book
	for _, c := range candidates {
		if normalizeForMatch(c.Title) != wantTitle {
			continue
		}
		if len(wantAuthors) == 0 {
			similar = append(similar, c)
			continue
		}
		for _, name := range strings.Split(c.AuthorNames, ", ") {
			last := strings.Fields(name)
			if len(last) > 0 && wantAuthors[normalizeForMatch(last[0])] {
				similar = append(similar, c)
				break
			}
		}
	}
	return similar, nil
}

// BookMergePair is a pending loser→winner consolidation pair awaiting its
// users-database merge.
type BookMergePair struct {
	Loser  int64
	Winner int64
}

// InsertPendingBookMergesTx records consolidation pairs inside the
// publication transaction. Publication may otherwise lose pairs: the catalog
// commits before the users-db merge runs, and a crash in between destroys
// the in-memory mapping (a repeated import finds the new keys and cannot
// reconstruct it). Row ids keep publication order, so a replay of chains
// (a winner that is itself a later publish's loser) runs winnable-first.
func (s *Store) InsertPendingBookMergesTx(ctx context.Context, tx *sql.Tx, pairs []BookMergePair) error {
	if len(pairs) == 0 {
		return nil
	}
	for _, p := range pairs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO pending_book_merges (loser_id, winner_id) VALUES (?, ?)
			ON CONFLICT (loser_id) DO UPDATE SET winner_id = excluded.winner_id`, p.Loser, p.Winner); err != nil {
			return err
		}
	}
	return nil
}

// PendingBookMerges returns the stored pairs in publication order.
func (s *Store) PendingBookMerges(ctx context.Context) ([]BookMergePair, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT loser_id, winner_id FROM pending_book_merges ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pairs []BookMergePair
	for rows.Next() {
		var p BookMergePair
		if err := rows.Scan(&p.Loser, &p.Winner); err != nil {
			return nil, err
		}
		pairs = append(pairs, p)
	}
	return pairs, rows.Err()
}

// ClearPendingBookMerges acknowledges pairs whose users merge succeeded.
// Only the given loser ids are removed so pairs recorded since the last
// read remain pending for the next pass.
func (s *Store) ClearPendingBookMerges(ctx context.Context, losers []int64) error {
	if len(losers) == 0 {
		return nil
	}
	placeholders := strings.Repeat("?,", len(losers)-1) + "?"
	args := make([]any, len(losers))
	for i, id := range losers {
		args[i] = id
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM pending_book_merges WHERE loser_id IN (`+placeholders+`)`, args...)
	return err
}

// BooksByIDs returns books in the order of the given ids (missing
// and deleted ones are skipped).
func (s *Store) BooksByIDs(ctx context.Context, ids []int64, libraryIDs ...string) ([]Book, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(ids)-1) + "?"
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	where, args := addLibraryScope(`b.id IN (`+placeholders+`)`, "b", scopeLibraryID(libraryIDs), args)
	books, err := s.queryBooks(ctx, where, "", 0, 0, args...)
	if err != nil {
		return nil, err
	}
	return orderBooksByID(ids, books), nil
}

// BookFilesByIDs returns files of existing books for bulk serving.
func (s *Store) BookFilesByIDs(ctx context.Context, ids []int64) ([]BookFile, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(ids)-1) + "?"
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.readDB.QueryContext(ctx, `
		SELECT b.id, b.title, fo.name, b.file, b.ext, b.size
		FROM books b JOIN folders fo ON fo.id = b.folder_id
		WHERE b.deleted = 0 AND b.missing = 0
		  AND EXISTS (SELECT 1 FROM libraries l WHERE l.id = b.library_id AND l.enabled = 1)
		  AND b.id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []BookFile
	for rows.Next() {
		var f BookFile
		if err := rows.Scan(&f.ID, &f.Title, &f.Folder, &f.File, &f.Ext, &f.Size); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}
