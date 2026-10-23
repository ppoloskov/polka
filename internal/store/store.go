// Package store is the storage layer: SQLite (WAL, FTS5) and operations on it.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/vestigiumincaligne/polka/internal/sqlitedrv"
)

type Store struct {
	db     *sql.DB
	readDB *sql.DB

	countsInit chan struct{} // closed when the background counts seed finishes

	// genreIDs caches visible per-genre member id lists; dropped whenever
	// visibility changes (see MaintainCounts / publication). The generation
	// counter guards installs raced against a concurrent drop.
	genreIDs struct {
		mu    sync.Mutex
		lists map[string][]int64
		gen   uint64
	}
}

func Open(path string) (*Store, error) {
	opts := sqlitedrv.Options{
		WAL: true, SyncNormal: true, BusyTimeout: 10000, ForeignKeys: true,
		// 64 MiB page cache per connection: the catalog is far larger than
		// the SQLite default cache, and hot read paths re-read the same pages.
		CacheSizeKiB: 65536,
	}
	db, err := sqlitedrv.Open(path, opts)
	if err != nil {
		return nil, err
	}
	// Keep every writer on one connection so SQLite writes stay serialized.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	// Pending book-reference merges bridge the catalog and users databases:
	// consolidation pairs are recorded inside the publication transaction and
	// acknowledged only after the users merge applied them (mergeBookRefs).
	// The table is created lazily instead of through a schema-version bump so
	// a rolled-back older binary runs unaffected, and nothing here blocks a
	// database that never consolidates.
	if _, err := db.ExecContext(context.Background(), `CREATE TABLE IF NOT EXISTS pending_book_merges (
		id        INTEGER PRIMARY KEY,
		loser_id  INTEGER NOT NULL UNIQUE,
		winner_id INTEGER NOT NULL
	)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create pending book merges table: %w", err)
	}
	// Partial shelf indexes: built-in shelves order by id/rate on live rows;
	// without these the planner walks full (library_id-agnostic) indexes and
	// applies the availability filter per candidate, which dominated deep
	// pagination on the real catalog (281ms at offset 60000 vs ~20ms).
	// A missing index merely costs speed; errors surface on the first query.
	for _, idx := range []string{
		`CREATE INDEX IF NOT EXISTS idx_books_added_live ON books (added, id) WHERE deleted = 0 AND missing = 0`,
		`CREATE INDEX IF NOT EXISTS idx_books_rate_live_4 ON books (lib_rate, id) WHERE deleted = 0 AND missing = 0 AND lib_rate >= 4`,
		`CREATE INDEX IF NOT EXISTS idx_books_rate_live_mid ON books (lib_rate, id) WHERE deleted = 0 AND missing = 0 AND lib_rate BETWEEN 1 AND 3`,
	} {
		if _, err := db.ExecContext(context.Background(), idx); err != nil {
			db.Close()
			return nil, fmt.Errorf("ensure book listing index: %w", err)
		}
	}
	// In-memory databases are connection-local unless opened with a shared
	// cache URI. Reuse the writer in that case; file databases get a reader
	// pool: multiple WAL readers scale, while one reader connection would
	// serialize every read behind the slowest query and turn concurrent page
	// loads, cover lookups and updates into a queue (measured ~10x p95
	// blowout on the real catalog at 8 concurrent requests). Reader handles
	// carry a smaller page cache (16 MiB): eight connections at the
	// writer's 64 MiB would tie up half a gigabyte; reads hit different
	// regions of the file and lean on the OS page cache instead.
	if strings.Contains(path, ":memory:") {
		s.readDB = db
	} else {
		readerOpts := opts
		readerOpts.CacheSizeKiB = 16384
		reader, err := sqlitedrv.Open(path, readerOpts)
		if err != nil {
			db.Close()
			return nil, err
		}
		reader.SetMaxOpenConns(8)
		s.readDB = reader
	}
	// Seed the maintained totals in the background so the first request on
	// an upgraded database normally finds them ready (they are durable in
	// meta; later starts early-return). A concurrent read initializes the
	// same values transactionally instead of writing a stale snapshot back.
	s.countsInit = make(chan struct{})
	go func() {
		defer close(s.countsInit)
		s.EnsureCountsInitialized(context.Background())
	}()
	return s, nil
}

func (s *Store) Close() error {
	<-s.countsInit // the seed writes before the connections go away
	readErr := s.readDB.Close()
	if s.readDB == s.db {
		return readErr
	}
	return errors.Join(readErr, s.db.Close())
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", version, schemaVersion)
	}
	if version == schemaVersion {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if version < 1 {
		if _, err := tx.Exec(schemaV1); err != nil {
			return err
		}
		for _, idx := range schemaIndexes {
			if _, err := tx.Exec(idx); err != nil {
				return err
			}
		}
		version = 5
	}
	if version == 1 {
		if _, err := tx.Exec(schemaV2); err != nil {
			return err
		}
		version = 2
	}
	if version == 2 {
		if _, err := tx.Exec(schemaV3); err != nil {
			return err
		}
		version = 3
	}
	if version == 3 {
		if _, err := tx.Exec(schemaV4); err != nil {
			return err
		}
		version = 4
	}
	if version == 4 {
		if _, err := tx.Exec(schemaV5); err != nil {
			return err
		}
		version = 5
	}
	if version == 5 {
		if _, err := tx.Exec(schemaV6); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) BookCount(ctx context.Context) (int, error) {
	var n int
	err := s.readDB.QueryRowContext(ctx, `
		SELECT count(*) FROM books b JOIN libraries l ON l.id = b.library_id
		WHERE b.deleted = 0 AND b.missing = 0 AND l.enabled = 1`).Scan(&n)
	return n, err
}

// VisibleBookCount is the home/config total. The value is maintained with
// the catalog: publication and direct mutations record the exact number in
// meta (see MaintainCounts), so reads are constant-time even on a 1.6M-row
// catalog where an inline count measured 1.4-1.7s. A database without the
// recorded value (first start after the upgrade) initializes it once —
// transactionally, so it can never overwrite totals a concurrent publisher
// committed later.
func (s *Store) VisibleBookCount(ctx context.Context) (int, error) {
	if err := s.EnsureCountsInitialized(ctx); err != nil {
		return 0, err
	}
	if v := s.GetMeta(ctx, metaKeysVisibleCount, ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n, nil
		}
	}
	return s.BookCount(ctx)
}

// EnsureCountsInitialized fills the maintained totals (visible book count
// and per-genre counts) when the database has none yet: publication records
// them with every commit, but a database upgraded to this code carries no
// values until the first publication, so the initialization computes them
// once, transactionally. Concurrent initializers serialize on the writer
// connection and the loser finds the values present; the compute runs in
// the same transaction as the write, so a read-snapshot result can never
// clobber newer totals committed meanwhile. Open starts it in the
// background so the first request on an upgraded database normally finds
// the values ready.
func (s *Store) EnsureCountsInitialized(ctx context.Context) error {
	if s.metaHoldsCounts(ctx) {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if s.metaHoldsCountsTx(ctx, tx) {
		return nil
	}
	if err := s.maintainCountsTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// metaHoldsCounts reports whether both maintained totals are present.
func (s *Store) metaHoldsCounts(ctx context.Context) bool {
	var present int
	err := s.readDB.QueryRowContext(ctx, `
		SELECT count(*) FROM meta WHERE key IN (?, ?)`,
		metaKeysVisibleCount, metaKeysGenreCounts).Scan(&present)
	return err == nil && present >= 2
}

func (s *Store) metaHoldsCountsTx(ctx context.Context, tx *sql.Tx) bool {
	var present int
	err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM meta WHERE key IN (?, ?)`,
		metaKeysVisibleCount, metaKeysGenreCounts).Scan(&present)
	return err == nil && present >= 2
}

// MaintainCounts recomputes the visible book total and the per-genre visible
// book counts and records both in meta so reads stay constant-time. The
// values normally ride the publication transaction (maintainCountsTx); a
// direct call recomputes standalone. On a 1.6M-book catalog this costs a
// couple of seconds of writer time, invisible to readers (WAL).
// Visibility-affecting changes also drop the cached per-genre member lists.
func (s *Store) MaintainCounts(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.maintainCountsTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.dropGenreVisibleIDs()
	return nil
}

// maintainCountsTx records both count families inside an open transaction.
func (s *Store) maintainCountsTx(ctx context.Context, tx *sql.Tx) error {
	var total int
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM books b WHERE b.deleted = 0 AND `+availableBook).Scan(&total); err != nil {
		return err
	}
	if err := setMetaTx(ctx, tx, metaKeysVisibleCount, strconv.Itoa(total)); err != nil {
		return err
	}
	type entry struct {
		Code  string `json:"Code"`
		Books int    `json:"Books"`
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT g.code, count(*)
		FROM genres g
		JOIN book_genres bg ON bg.genre_id = g.id
		JOIN books b ON b.id = bg.book_id AND b.deleted = 0 AND `+availableBook+`
		GROUP BY g.id
		HAVING count(*) > 0`)
	if err != nil {
		return err
	}
	defer rows.Close()
	counts := make([]entry, 0, 384)
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.Code, &e.Books); err != nil {
			return err
		}
		counts = append(counts, e)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	b, err := json.Marshal(counts)
	if err != nil {
		return err
	}
	return setMetaTx(ctx, tx, metaKeysGenreCounts, string(b))
}

func (s *Store) GetMeta(ctx context.Context, key, def string) string {
	var v string
	if err := s.readDB.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v); err != nil {
		return def
	}
	return v
}

func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// setMetaTx writes a meta value inside the given transaction (nil = the
// standalone writer connection).
func setMetaTx(ctx context.Context, tx *sql.Tx, key, value string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
