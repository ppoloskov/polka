package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vestigiumincaligne/polka/internal/sqlitedrv"
)

// Snapshot stages source records in a temporary SQLite database so an update
// does not need to keep the complete catalog in memory.
type Snapshot struct {
	db     *sql.DB
	tx     *sql.Tx
	insert *sql.Stmt
	dir    string
	poison error
	sealed bool
	closed bool
}

// UpdateStats counts new keys, newly absent keys, availability transitions,
// and keys whose availability stayed the same.
type UpdateStats struct {
	Added     int
	Removed   int
	Changed   int
	Unchanged int
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
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_books_library_source ON books(library_id, source_key)`); err != nil {
		return stats, err
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
	// Mark currently present source rows so the staged keys can restore matches
	// and the rows left marked at the end are newly missing.
	if _, err := tx.ExecContext(ctx, `UPDATE books SET missing=2 WHERE library_id=? AND source_key!='' AND missing=0`, libraryID); err != nil {
		return stats, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM book_search WHERE rowid IN (SELECT id FROM books WHERE library_id=? AND source_key!='')`, libraryID); err != nil {
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
			id, err := addBookTx(ctx, tx, &book)
			if err != nil {
				return stats, err
			}
			if book.Deleted {
				if _, err := tx.ExecContext(ctx, `UPDATE books SET missing=1 WHERE id=?`, id); err != nil {
					return stats, err
				}
				if _, err := tx.ExecContext(ctx, `DELETE FROM book_search WHERE rowid=?`, id); err != nil {
					return stats, err
				}
			}
			stats.Added++
			continue
		}
		if err != nil {
			return stats, err
		}
		wasMissing := missing == 1
		if _, err := tx.ExecContext(ctx, `UPDATE books SET missing=? WHERE id=?`, boolToInt(book.Deleted), id); err != nil {
			return stats, err
		}
		if !book.Deleted {
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
	if err := ctx.Err(); err != nil {
		return stats, err
	}
	if err := tx.Commit(); err != nil {
		return stats, err
	}
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
