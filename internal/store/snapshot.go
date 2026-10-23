package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/vestigiumincaligne/polka/internal/sqlitedrv"
)

// Snapshot stages source records in a temporary SQLite database so an update
// does not need to keep the complete catalog in memory.
type Snapshot struct {
	db                       *sql.DB
	tx                       *sql.Tx
	insert                   *sql.Stmt
	dir                      string
	poison                   error
	sealed                   bool
	closed                   bool
	inpxName                 string
	inpxHash                 string
	collection               string
	sourcePath               string
	markLegacyUnkeyedMissing bool
}

// SetInpxSource records provenance to commit with an INPX snapshot. When
// markLegacyUnkeyedMissing is true, identity-unknown legacy catalog rows are
// retained as unavailable; hashed web uploads are left alone.
func (s *Snapshot) SetInpxSource(name, hash, collection, sourcePath string, markLegacyUnkeyedMissing bool) {
	s.inpxName, s.inpxHash = name, hash
	s.collection, s.sourcePath = collection, sourcePath
	s.markLegacyUnkeyedMissing = markLegacyUnkeyedMissing
}

// UpdateStats counts new keys, newly absent keys, availability transitions,
// and keys whose availability stayed the same.
type UpdateStats struct {
	Added     int
	Removed   int
	Changed   int
	Unchanged int
}

// LibraryBookCount counts visible catalog rows belonging to one library.
func (s *Store) LibraryBookCount(ctx context.Context, libraryID string) (int, error) {
	var n int
	err := s.readDB.QueryRowContext(ctx, `SELECT count(*) FROM books WHERE library_id=? AND deleted=0 AND missing=0`, libraryID).Scan(&n)
	return n, err
}

// NewSnapshot creates a temporary SQLite-backed staging area under dir. An
// empty dir uses the operating system temporary directory.
func NewSnapshot(dir string) (*Snapshot, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	tempDir, err := os.MkdirTemp(dir, "polka-snapshot-")
	if err != nil {
		return nil, err
	}
	db, err := sqlitedrv.Open(filepath.Join(tempDir, "snapshot.db"), sqlitedrv.Options{BusyTimeout: 10000})
	if err != nil {
		os.RemoveAll(tempDir)
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE records (source_key TEXT PRIMARY KEY, book_json BLOB NOT NULL) WITHOUT ROWID`); err != nil {
		db.Close()
		os.RemoveAll(tempDir)
		return nil, err
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		db.Close()
		os.RemoveAll(tempDir)
		return nil, err
	}
	insert, err := tx.Prepare(`INSERT INTO records (source_key, book_json) VALUES (?, ?)`)
	if err != nil {
		tx.Rollback()
		db.Close()
		os.RemoveAll(tempDir)
		return nil, err
	}
	return &Snapshot{db: db, tx: tx, insert: insert, dir: tempDir}, nil
}

// Add stages one source record. key must represent immutable source identity;
// source metadata corrections must use a new key. Any invalid row poisons the
// snapshot, so a caller cannot accidentally publish an incomplete enumeration.
func (s *Snapshot) Add(ctx context.Context, key string, b *BookInput) error {
	if s == nil || s.closed {
		return errors.New("snapshot is closed")
	}
	if s.poison != nil {
		return s.poison
	}
	if s.sealed {
		return s.fail(errors.New("snapshot is already sealed"))
	}
	if err := ctx.Err(); err != nil {
		return s.fail(err)
	}
	if strings.TrimSpace(key) == "" {
		return s.fail(errors.New("snapshot source key is required"))
	}
	if b == nil {
		return s.fail(errors.New("snapshot book is required"))
	}
	data, err := json.Marshal(b)
	if err != nil {
		return s.fail(err)
	}
	if _, err := s.insert.ExecContext(ctx, key, data); err != nil {
		return s.fail(fmt.Errorf("stage source key %q: %w", key, err))
	}
	return nil
}

func (s *Snapshot) fail(err error) error {
	if s.poison == nil {
		s.poison = err
	}
	return s.poison
}

func (s *Snapshot) seal() error {
	if s == nil || s.closed {
		return errors.New("snapshot is closed")
	}
	if s.poison != nil {
		return s.poison
	}
	if s.sealed {
		return nil
	}
	if err := s.insert.Close(); err != nil {
		return s.fail(err)
	}
	s.insert = nil
	if err := s.tx.Commit(); err != nil {
		return s.fail(err)
	}
	s.tx = nil
	s.sealed = true
	return nil
}

// Close removes the temporary snapshot database and its directory.
func (s *Snapshot) Close() error {
	if s == nil || s.closed {
		return nil
	}
	s.closed = true
	if s.insert != nil {
		s.insert.Close()
	}
	if s.tx != nil {
		s.tx.Rollback()
	}
	err := s.db.Close()
	if removeErr := os.RemoveAll(s.dir); err == nil {
		err = removeErr
	}
	return err
}

// PublishLibrary atomically applies a complete source snapshot to one library.
// Source keys are immutable identities; matching rows keep their catalog data.
func (s *Store) PublishLibrary(ctx context.Context, libraryID string, snapshot *Snapshot, allowEmpty bool) (stats UpdateStats, resultErr error) {
	defer func() {
		if resultErr != nil {
			stats = UpdateStats{}
		}
	}()
	if snapshot == nil {
		return stats, errors.New("snapshot is required")
	}
	if err := snapshot.seal(); err != nil {
		return stats, err
	}
	var staged int
	if err := snapshot.db.QueryRowContext(ctx, `SELECT count(*) FROM records`).Scan(&staged); err != nil {
		return stats, err
	}
	if staged == 0 && !allowEmpty {
		return stats, errors.New("empty library snapshot is not allowed")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return stats, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM libraries WHERE id=?`, libraryID).Scan(&exists); err != nil {
		return stats, err
	}
	if exists == 0 {
		return stats, ErrNotFound
	}
	var highWater int64
	if err := tx.QueryRowContext(ctx, `SELECT CAST(value AS INTEGER) FROM meta WHERE key=?`, bookIDHighWaterKey).Scan(&highWater); err != nil {
		return stats, fmt.Errorf("read book ID high-water mark: %w", err)
	}
	initialHighWater := highWater
	var writer *ImportSession
	var maxAuthorID, maxSeriesID int64
	// Mark currently present source rows so the staged keys can restore matches
	// and the rows left marked at the end are newly missing.
	if _, err := tx.ExecContext(ctx, `UPDATE books SET missing=2 WHERE library_id=? AND source_key!='' AND missing=0`, libraryID); err != nil {
		return stats, err
	}
	rows, err := snapshot.db.QueryContext(ctx, `SELECT source_key, book_json FROM records ORDER BY source_key`)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		var key string
		var data []byte
		if err := rows.Scan(&key, &data); err != nil {
			return stats, err
		}
		var book BookInput
		if err := json.Unmarshal(data, &book); err != nil {
			return stats, fmt.Errorf("read staged source key %q: %w", key, err)
		}
		book.LibraryID = libraryID
		book.SourceKey = key
		var id int64
		var missing int
		err := tx.QueryRowContext(ctx, `SELECT id, missing FROM books WHERE library_id=? AND source_key=?`, libraryID, key).Scan(&id, &missing)
		if errors.Is(err, sql.ErrNoRows) {
			if highWater == math.MaxInt64 {
				return stats, errors.New("book ID range is exhausted")
			}
			if writer == nil {
				writer, maxAuthorID, maxSeriesID, err = newImportSessionForTx(s, ctx, tx)
				if err != nil {
					return stats, err
				}
			}
			highWater++
			sourceDeleted := book.Deleted
			book.Deleted = false
			if err := writer.addBook(highWater, &book); err != nil {
				return stats, err
			}
			if sourceDeleted {
				if _, err := tx.ExecContext(ctx, `UPDATE books SET missing=1 WHERE id=?`, highWater); err != nil {
					return stats, err
				}
			}
			stats.Added++
			continue
		}
		if err != nil {
			return stats, err
		}
		wasMissing := missing != 2
		if _, err := tx.ExecContext(ctx, `UPDATE books SET missing=? WHERE id=?`, boolToInt(book.Deleted), id); err != nil {
			return stats, err
		}
		if book.Deleted {
			if _, err := tx.ExecContext(ctx, `DELETE FROM book_search WHERE rowid=?`, id); err != nil {
				return stats, err
			}
		} else if wasMissing {
			if err := restoreBookSearchTx(ctx, tx, id); err != nil {
				return stats, err
			}
		}
		if wasMissing != book.Deleted {
			stats.Changed++
		} else {
			stats.Unchanged++
		}
	}
	if err := rows.Err(); err != nil {
		return stats, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM books WHERE library_id=? AND source_key!='' AND missing=2`, libraryID).Scan(&stats.Removed); err != nil {
		return stats, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE books SET missing=1 WHERE library_id=? AND missing=2`, libraryID); err != nil {
		return stats, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM book_search WHERE rowid IN (SELECT id FROM books WHERE library_id=? AND source_key!='' AND missing=1)`, libraryID); err != nil {
		return stats, err
	}
	if libraryID == "legacy" && snapshot.markLegacyUnkeyedMissing {
		res, err := tx.ExecContext(ctx, `UPDATE books SET missing=1 WHERE library_id='legacy' AND source_key='' AND file_hash='' AND missing=0`)
		if err != nil {
			return stats, err
		}
		removed, err := res.RowsAffected()
		if err != nil {
			return stats, err
		}
		stats.Removed += int(removed)
		if _, err := tx.ExecContext(ctx, `DELETE FROM book_search WHERE rowid IN (SELECT id FROM books WHERE library_id='legacy' AND source_key='' AND file_hash='' AND missing=1)`); err != nil {
			return stats, err
		}
	}
	if highWater != initialHighWater {
		if _, err := tx.ExecContext(ctx, `UPDATE meta SET value=? WHERE key=?`, fmt.Sprint(highWater), bookIDHighWaterKey); err != nil {
			return stats, fmt.Errorf("save book ID high-water mark: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO authors_search (rowid,name) SELECT id,trim(last_name || ' ' || first_name || ' ' || middle_name) FROM authors WHERE id>?`, maxAuthorID); err != nil {
			return stats, fmt.Errorf("index new authors: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO series_search (rowid,title) SELECT id,title FROM series WHERE id>?`, maxSeriesID); err != nil {
			return stats, fmt.Errorf("index new series: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO book_search (rowid,title,authors,series)
			SELECT b.id,b.title,
			       coalesce((SELECT group_concat(trim(a.last_name || ' ' || a.first_name || ' ' || a.middle_name), ' ')
			                 FROM book_authors ba JOIN authors a ON a.id=ba.author_id WHERE ba.book_id=b.id),''),
			       coalesce((SELECT group_concat(s.title, ' ') FROM book_series bs JOIN series s ON s.id=bs.series_id WHERE bs.book_id=b.id),'')
			FROM books b WHERE b.id>? AND b.id<=? AND b.library_id=? AND b.deleted=0 AND b.missing=0`, initialHighWater, highWater, libraryID); err != nil {
			return stats, fmt.Errorf("index new books: %w", err)
		}
	}
	if snapshot.inpxHash != "" {
		metadata := map[string]string{
			"inpx_filename": snapshot.inpxName,
			"inpx_sha256":   snapshot.inpxHash,
		}
		if snapshot.collection != "" {
			metadata["collection_name"] = snapshot.collection
		}
		for key, value := range metadata {
			if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key,value) VALUES (?,?) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, "library."+libraryID+"."+key, value); err != nil {
				return stats, err
			}
			if libraryID == "legacy" {
				if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key,value) VALUES (?,?) ON CONFLICT (key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
					return stats, err
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE libraries SET source_path=? WHERE id=?`, snapshot.sourcePath, libraryID); err != nil {
			return stats, err
		}
	}
	// Covers can change on any committed publish (the server resets the
	// cover cache), so bump the per-library cover generation backing the
	// cover ETag validator regardless of the diff content.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO meta (key, value) VALUES (?, '1')
		ON CONFLICT (key) DO UPDATE SET value = CAST(CAST(meta.value AS INTEGER) + 1 AS TEXT)`, "library."+libraryID+".cover_gen"); err != nil {
		return stats, err
	}
	if err := ctx.Err(); err != nil {
		return stats, err
	}
	if err := tx.Commit(); err != nil {
		return stats, err
	}
	// Refresh planner statistics after a large publish. The catalog is
	// already committed, so an ANALYZE failure is not fatal.
	_, _ = s.db.ExecContext(ctx, `ANALYZE`)
	return stats, nil
}

func restoreBookSearchTx(ctx context.Context, tx *sql.Tx, bookID int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO book_search (rowid,title,authors,series)
		SELECT b.id,b.title,
		       coalesce((SELECT group_concat(trim(a.last_name || ' ' || a.first_name || ' ' || a.middle_name), ' ')
		                 FROM book_authors ba JOIN authors a ON a.id=ba.author_id WHERE ba.book_id=b.id),''),
		       coalesce((SELECT group_concat(s.title, ' ') FROM book_series bs JOIN series s ON s.id=bs.series_id WHERE bs.book_id=b.id),'')
		FROM books b WHERE b.id=? AND b.deleted=0 AND b.missing=0`, bookID)
	return err
}
