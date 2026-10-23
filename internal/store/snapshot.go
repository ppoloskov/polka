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
	"sort"
	"strings"

	"github.com/vestigiumincaligne/polka/internal/sqlitedrv"
)

// Snapshot stages source records in a temporary SQLite database so an update
// does not need to keep the complete catalog in memory.
type Snapshot struct {
	db                       *sql.DB
	tx                       *sql.Tx
	insert                   *sql.Stmt
	insertLine               *sql.Stmt
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
	// ConsolidatedIDs maps retired per-series inpx:v1 row IDs to the kept
	// (lowest) ID when a publish rekeys them into one inpx:v2 file group.
	// Never serialized into status payloads.
	ConsolidatedIDs map[int64]int64 `json:"-"`
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
	if _, err := db.Exec(`CREATE TABLE records (source_key TEXT PRIMARY KEY, book_json BLOB NOT NULL, members TEXT NOT NULL DEFAULT '') WITHOUT ROWID`); err != nil {
		db.Close()
		os.RemoveAll(tempDir)
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE inpx_lines (file_key TEXT NOT NULL, hash TEXT NOT NULL, book_json BLOB NOT NULL)`); err != nil {
		db.Close()
		os.RemoveAll(tempDir)
		return nil, err
	}
	if _, err := db.Exec(`CREATE INDEX idx_inpx_lines_file_key ON inpx_lines (file_key)`); err != nil {
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
	insert, err := tx.Prepare(`INSERT INTO records (source_key, book_json, members) VALUES (?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		db.Close()
		os.RemoveAll(tempDir)
		return nil, err
	}
	insertLine, err := tx.Prepare(`INSERT INTO inpx_lines (file_key, hash, book_json) VALUES (?, ?, ?)`)
	if err != nil {
		insert.Close()
		tx.Rollback()
		db.Close()
		os.RemoveAll(tempDir)
		return nil, err
	}
	return &Snapshot{db: db, tx: tx, insert: insert, insertLine: insertLine, dir: tempDir}, nil
}

// Add stages one source record. key must represent immutable source identity;
// source metadata corrections must use a new key. Any invalid row poisons the
// snapshot, so a caller cannot accidentally publish an incomplete enumeration.
// Add stages one source record. key must represent immutable source identity;
// a matching key at publish keeps the row, a new key adds one.
func (s *Snapshot) Add(ctx context.Context, key string, b *BookInput) error {
	return s.add(ctx, key, b, "")
}

// add is Add with optional group member hashes (INPX file groups): publish
// uses them to recognize a surviving row that already holds the exact record.
func (s *Snapshot) add(ctx context.Context, key string, b *BookInput, members string) error {
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
	if _, err := s.insert.ExecContext(ctx, key, data, members); err != nil {
		return s.fail(fmt.Errorf("stage source key %q: %w", key, err))
	}
	return nil
}

// StagedLine is one staged INPX index line with its per-line content hash.
type StagedLine struct {
	Hash string
	Book BookInput
}

// AddInpxLine stages one INPX index line for later grouping by fileKey
// (Flibusta repeats a multi-series file as one line per series). Lines share
// the Add poisoning rules: any invalid row poisons the snapshot.
func (s *Snapshot) AddInpxLine(ctx context.Context, fileKey, hash string, book BookInput) error {
	if s == nil || s.closed {
		return errors.New("snapshot is closed")
	}
	if s.poison != nil {
		return s.poison
	}
	if s.sealed {
		return s.fail(errors.New("snapshot is already sealed"))
	}
	if s.insertLine == nil {
		return s.fail(errors.New("snapshot INPX lines are already collapsed"))
	}
	if err := ctx.Err(); err != nil {
		return s.fail(err)
	}
	if strings.TrimSpace(fileKey) == "" {
		return s.fail(errors.New("snapshot INPX file key is required"))
	}
	if strings.TrimSpace(hash) == "" {
		return s.fail(errors.New("snapshot INPX line hash is required"))
	}
	data, err := json.Marshal(&book)
	if err != nil {
		return s.fail(err)
	}
	if _, err := s.insertLine.ExecContext(ctx, fileKey, hash, data); err != nil {
		return s.fail(fmt.Errorf("stage INPX line %q: %w", fileKey, err))
	}
	return nil
}

// CollapseInpxLines streams staged INPX lines ordered by file key, folds each
// file's lines into one staged record through merge, and drops the line store.
// Memory stays proportional to the largest per-file group. Merge or staging
// failures poison the snapshot like an invalid Add.
func (s *Snapshot) CollapseInpxLines(ctx context.Context, merge func(members []StagedLine) (key string, book BookInput, err error)) (int, error) {
	if s == nil || s.closed {
		return 0, errors.New("snapshot is closed")
	}
	if s.poison != nil {
		return 0, s.poison
	}
	if s.sealed {
		return 0, s.fail(errors.New("snapshot is already sealed"))
	}
	if s.insertLine == nil {
		return 0, s.fail(errors.New("snapshot INPX lines are already collapsed"))
	}
	if err := s.insertLine.Close(); err != nil {
		return 0, s.fail(err)
	}
	s.insertLine = nil
	rows, err := s.tx.QueryContext(ctx, `SELECT file_key, hash, book_json FROM inpx_lines ORDER BY file_key, rowid`)
	if err != nil {
		return 0, s.fail(err)
	}
	groups := 0
	var fileKey string
	var members []StagedLine
	flush := func() error {
		if len(members) == 0 {
			return nil
		}
		key, book, err := merge(members)
		if err != nil {
			return s.fail(err)
		}
		hashes := make([]string, 0, len(members))
		for _, member := range members {
			hashes = append(hashes, member.Hash)
		}
		sort.Strings(hashes)
		memberJSON, err := json.Marshal(hashes)
		if err != nil {
			return s.fail(err)
		}
		if err := s.add(ctx, key, &book, string(memberJSON)); err != nil {
			return err
		}
		groups++
		members = members[:0]
		return nil
	}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			rows.Close()
			return groups, s.fail(err)
		}
		var lineKey, hash string
		var data []byte
		if err := rows.Scan(&lineKey, &hash, &data); err != nil {
			rows.Close()
			return groups, s.fail(err)
		}
		if lineKey != fileKey {
			if err := flush(); err != nil {
				rows.Close()
				return groups, err
			}
			fileKey = lineKey
		}
		var book BookInput
		if err := json.Unmarshal(data, &book); err != nil {
			rows.Close()
			return groups, s.fail(fmt.Errorf("read staged INPX line %q: %w", lineKey, err))
		}
		members = append(members, StagedLine{Hash: hash, Book: book})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return groups, s.fail(err)
	}
	if err := rows.Close(); err != nil {
		return groups, s.fail(err)
	}
	if err := flush(); err != nil {
		return groups, err
	}
	if _, err := s.tx.ExecContext(ctx, `DROP TABLE inpx_lines`); err != nil {
		return groups, s.fail(err)
	}
	return groups, nil
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
	if s.insertLine != nil {
		if err := s.insertLine.Close(); err != nil {
			return s.fail(err)
		}
		s.insertLine = nil
	}
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
	if s.insertLine != nil {
		s.insertLine.Close()
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

// INPX source key versions: inpx:v1 fingerprinted every index line, inpx:v2
// fingerprints the sorted member hashes of one file's group of lines.
const inpxV1KeyPrefix = "inpx:v1:"
const inpxV2KeyPrefix = "inpx:v2:"

// ftsRebuildThreshold switches search-index maintenance from targeted deletes
// to a wholesale rebuild once a publish invalidates more rows than this.
// Interleaved per-row FTS5 deletes cost milliseconds each at catalog scale
// (segment merges grow with the index); delete-all plus a bulk reindex of a
// large library finishes in minutes regardless of the invalidation count.
// Variable (not const) so tests can lower it.
var ftsRebuildThreshold int64 = 10000

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
	// Search-index rows this publish must delete are collected here and
	// handled after the loop: per-row FTS5 deletes interleaved with writes
	// are the dominant publish cost at scale, so they are either executed as
	// one set-based delete or replaced by a wholesale rebuild (see below).
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE polka_fts_dirty (id INTEGER PRIMARY KEY) WITHOUT ROWID`); err != nil {
		return stats, err
	}
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
	// Staged inpx:v2 groups rebind same-file inpx:v1 rows (see rekeyInpxGroup).
	// The probe runs against an indexed temp snapshot of the library's v1 rows,
	// built lazily and skipped for libraries without any.
	var hasInpxV1Rows bool
	var oneRow int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM books WHERE library_id=? AND source_key GLOB 'inpx:v1:*' LIMIT 1`, libraryID).Scan(&oneRow); err == nil {
		hasInpxV1Rows = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return stats, err
	}
	v1Probe := false
	rekeyed := 0
	// Consolidation pairs are recorded in this transaction (pending book
	// merges) so the users-db merge survives a crash after the commit.
	var pendingMerges []BookMergePair
	// Mark currently present source rows so the staged keys can restore matches
	// and the rows left marked at the end are newly missing.
	if _, err := tx.ExecContext(ctx, `UPDATE books SET missing=2 WHERE library_id=? AND source_key!='' AND missing=0`, libraryID); err != nil {
		return stats, err
	}
	rows, err := snapshot.db.QueryContext(ctx, `SELECT source_key, book_json, members FROM records ORDER BY source_key`)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		var key, membersJSON string
		var data []byte
		if err := rows.Scan(&key, &data, &membersJSON); err != nil {
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
		if errors.Is(err, sql.ErrNoRows) && hasInpxV1Rows && strings.HasPrefix(key, inpxV2KeyPrefix) {
			if !v1Probe {
				if err := prepareInpxV1Probe(ctx, tx, libraryID); err != nil {
					return stats, err
				}
				v1Probe = true
			}
			group, lookupErr := findInpxV1Group(ctx, tx, book.Folder, book.File, book.Ext)
			if lookupErr != nil {
				return stats, lookupErr
			}
			if len(group) > 0 {
				var members []string
				if membersJSON != "" {
					if err := json.Unmarshal([]byte(membersJSON), &members); err != nil {
						return stats, fmt.Errorf("read group members %q: %w", key, err)
					}
				}
				if len(members) == 1 && group[0].Key == inpxV1KeyPrefix+members[0] {
					// Key-swap fast path: the winner's v1 key is the content
					// hash of the group's only line, so the row already holds
					// exactly this record — including metadata learned after
					// the original import, which a content rewrite would
					// destroy. Only the identity changes.
					if _, err := tx.ExecContext(ctx, `UPDATE books SET source_key=?, missing=? WHERE id=?`, key, boolToInt(book.Deleted), group[0].ID); err != nil {
						return stats, err
					}
					if book.Deleted {
						if _, err := tx.ExecContext(ctx, `INSERT INTO polka_fts_dirty (id) VALUES (?)`, group[0].ID); err != nil {
							return stats, err
						}
					}
				} else {
					if writer == nil {
						writer, maxAuthorID, maxSeriesID, err = newImportSessionForTx(s, ctx, tx)
						if err != nil {
							return stats, err
						}
					}
					if err := rekeyInpxGroup(ctx, tx, writer, key, &book, group[0].ID); err != nil {
						return stats, err
					}
					if _, err := tx.ExecContext(ctx, `INSERT INTO polka_fts_dirty (id) VALUES (?)`, group[0].ID); err != nil {
						return stats, err
					}
				}
				rekeyed++
				// The rebound row now carries its v2 key; keep the temp probe
				// consistent so a same-(folder, file) probe cannot rebind it.
				// The predicate must cover the probe index prefix: an id-only
				// delete would full-scan the probe table once per group.
				if _, err := tx.ExecContext(ctx, `DELETE FROM polka_v1_files WHERE folder=? AND file=? AND ext=? AND id=?`, book.Folder, book.File, book.Ext, group[0].ID); err != nil {
					return stats, err
				}
				for _, loser := range group[1:] {
					if stats.ConsolidatedIDs == nil {
						stats.ConsolidatedIDs = make(map[int64]int64)
					}
					stats.ConsolidatedIDs[loser.ID] = group[0].ID
					pendingMerges = append(pendingMerges, BookMergePair{Loser: loser.ID, Winner: group[0].ID})
				}
				stats.Changed++
				continue
			}
		}
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
			if _, err := tx.ExecContext(ctx, `INSERT INTO polka_fts_dirty (id) VALUES (?)`, id); err != nil {
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
	if v1Probe {
		if _, err := tx.ExecContext(ctx, `DROP TABLE polka_v1_files`); err != nil {
			return stats, err
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM books WHERE library_id=? AND source_key!='' AND missing=2`, libraryID).Scan(&stats.Removed); err != nil {
		return stats, err
	}
	// Swept rows lose their search rows too; record the exact newly-swept set
	// before missing=2 flips to missing=1, instead of probing every row ever
	// swept by any publish.
	if _, err := tx.ExecContext(ctx, `INSERT INTO polka_fts_dirty SELECT id FROM books WHERE library_id=? AND source_key!='' AND missing=2`, libraryID); err != nil {
		return stats, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE books SET missing=1 WHERE library_id=? AND missing=2`, libraryID); err != nil {
		return stats, err
	}
	if libraryID == "legacy" && snapshot.markLegacyUnkeyedMissing {
		if _, err := tx.ExecContext(ctx, `INSERT INTO polka_fts_dirty SELECT id FROM books WHERE library_id='legacy' AND source_key='' AND file_hash='' AND missing=0`); err != nil {
			return stats, err
		}
		res, err := tx.ExecContext(ctx, `UPDATE books SET missing=1 WHERE library_id='legacy' AND source_key='' AND file_hash='' AND missing=0`)
		if err != nil {
			return stats, err
		}
		removed, err := res.RowsAffected()
		if err != nil {
			return stats, err
		}
		stats.Removed += int(removed)
	}
	var dirty int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM polka_fts_dirty`).Scan(&dirty); err != nil {
		return stats, err
	}
	if dirty > ftsRebuildThreshold {
		// Mass change: per-row FTS5 deletes cost more than reindexing every
		// live row. book_search is content-owning, so 'delete-all' is not
		// available; drop and recreate it instead (transactional DDL, same
		// definition as schema.go). This wipes the index across libraries, so
		// the rebuild below must stay library-agnostic.
		if _, err := tx.ExecContext(ctx, `DROP TABLE book_search`); err != nil {
			return stats, fmt.Errorf("drop search index: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `CREATE VIRTUAL TABLE book_search USING fts5(
			title,
			authors,
			series,
			tokenize = 'unicode61 remove_diacritics 2'
		)`); err != nil {
			return stats, fmt.Errorf("recreate search index: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO book_search (rowid,title,authors,series)
			SELECT b.id,b.title,
			       coalesce((SELECT group_concat(trim(a.last_name || ' ' || a.first_name || ' ' || a.middle_name), ' ')
			                 FROM book_authors ba JOIN authors a ON a.id=ba.author_id WHERE ba.book_id=b.id),''),
			       coalesce((SELECT group_concat(s.title, ' ') FROM book_series bs JOIN series s ON s.id=bs.series_id WHERE bs.book_id=b.id),'')
			FROM books b WHERE b.deleted=0 AND b.missing=0`); err != nil {
			return stats, fmt.Errorf("rebuild search index: %w", err)
		}
	} else {
		if dirty > 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM book_search WHERE rowid IN (SELECT id FROM polka_fts_dirty)`); err != nil {
				return stats, err
			}
		}
		if rekeyed > 0 {
			// Rekeyed winners lost their search rows above; rebuild them in a
			// single pass over the live rows that lack one.
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO book_search (rowid,title,authors,series)
				SELECT b.id,b.title,
				       coalesce((SELECT group_concat(trim(a.last_name || ' ' || a.first_name || ' ' || a.middle_name), ' ')
				                 FROM book_authors ba JOIN authors a ON a.id=ba.author_id WHERE ba.book_id=b.id),''),
				       coalesce((SELECT group_concat(s.title, ' ') FROM book_series bs JOIN series s ON s.id=bs.series_id WHERE bs.book_id=b.id),'')
				FROM books b WHERE b.library_id=? AND b.deleted=0 AND b.missing=0
				  AND NOT EXISTS (SELECT 1 FROM book_search WHERE book_search.rowid=b.id)`, libraryID); err != nil {
				return stats, fmt.Errorf("rebuild rekeyed search rows: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE polka_fts_dirty`); err != nil {
		return stats, err
	}
	// A consolidation-only publish allocates no book IDs but can still create
	// series/authors through the rekey path; index those too.
	if writer != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO authors_search (rowid,name) SELECT id,trim(last_name || ' ' || first_name || ' ' || middle_name) FROM authors WHERE id>?`, maxAuthorID); err != nil {
			return stats, fmt.Errorf("index new authors: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO series_search (rowid,title) SELECT id,title FROM series WHERE id>?`, maxSeriesID); err != nil {
			return stats, fmt.Errorf("index new series: %w", err)
		}
	}
	if highWater != initialHighWater {
		if _, err := tx.ExecContext(ctx, `UPDATE meta SET value=? WHERE key=?`, fmt.Sprint(highWater), bookIDHighWaterKey); err != nil {
			return stats, fmt.Errorf("save book ID high-water mark: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO book_search (rowid,title,authors,series)
			SELECT b.id,b.title,
			       coalesce((SELECT group_concat(trim(a.last_name || ' ' || a.first_name || ' ' || a.middle_name), ' ')
			                 FROM book_authors ba JOIN authors a ON a.id=ba.author_id WHERE ba.book_id=b.id),''),
			       coalesce((SELECT group_concat(s.title, ' ') FROM book_series bs JOIN series s ON s.id=bs.series_id WHERE bs.book_id=b.id),'')
			FROM books b WHERE b.id>? AND b.id<=? AND b.library_id=? AND b.deleted=0 AND b.missing=0
			  AND NOT EXISTS (SELECT 1 FROM book_search WHERE book_search.rowid=b.id)`, initialHighWater, highWater, libraryID); err != nil {
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
	// Consolidation pairs must survive the crash window between the catalog
	// commit and the users-db merge: recorded here, applied and acknowledged
	// after the commit (processPendingMerges), with idempotent replay as the
	// recovery mechanism.
	if err := s.InsertPendingBookMergesTx(ctx, tx, pendingMerges); err != nil {
		return stats, fmt.Errorf("record pending book merges: %w", err)
	}
	// Record the maintained totals in the same transaction so config and
	// genre reads are constant-time after the commit (the inline counters
	// cost seconds on a 1.6M-row catalog).
	if err := s.maintainCountsTx(ctx, tx); err != nil {
		return stats, fmt.Errorf("maintain counts: %w", err)
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
	// The committed state may differ from any in-memory genre member lists
	// built during or before this publish: drop them so subsequent reads
	// rebuild against the new catalog.
	s.dropGenreVisibleIDs()
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

// prepareInpxV1Probe snapshots the library's live per-series v1 rows into an
// indexed temp lookup so per-group consolidation probes are indexed reads
// instead of per-folder scans. The selection is the consolidation rule:
// any missing status matches, admin-deleted rows never do.
func prepareInpxV1Probe(ctx context.Context, tx *sql.Tx, libraryID string) error {
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS polka_v1_files`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE polka_v1_files (folder TEXT NOT NULL, file TEXT NOT NULL, ext TEXT NOT NULL, id INTEGER NOT NULL, source_key TEXT NOT NULL)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO polka_v1_files
		SELECT f.name, b.file, b.ext, b.id, b.source_key FROM books b JOIN folders f ON f.id=b.folder_id
		WHERE b.library_id=? AND b.source_key GLOB 'inpx:v1:*' AND b.deleted=0`, libraryID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `CREATE INDEX idx_polka_v1_files ON polka_v1_files (folder, file, ext, id)`)
	return err
}

// inpxV1Row is one live per-series v1 row of a file, with its key so a
// consolidation can recognize a row that already holds the staged record.
type inpxV1Row struct {
	ID  int64
	Key string
}

// findInpxV1Group returns the live per-series v1 rows of one file, lowest ID
// first: the exact consolidation candidates for a staged inpx:v2 group.
func findInpxV1Group(ctx context.Context, tx *sql.Tx, folder, file, ext string) ([]inpxV1Row, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, source_key FROM polka_v1_files WHERE folder=? AND file=? AND ext=? ORDER BY id`, folder, file, ext)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []inpxV1Row
	for rows.Next() {
		var row inpxV1Row
		if err := rows.Scan(&row.ID, &row.Key); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// rekeyInpxGroup binds the winner (lowest-ID v1 row of one file) to the staged
// v2 group content inside the publish transaction: relations and the search
// row are rebuilt from the merged book, and the row keeps its ID and folder.
// Remaining v1 rows of the file need no handling: their absent keys retire
// them to missing through the normal sweep.
func rekeyInpxGroup(ctx context.Context, tx *sql.Tx, writer *ImportSession, key string, book *BookInput, winner int64) error {
	for _, table := range []string{"book_authors", "book_genres", "book_keywords", "book_series"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE book_id=?`, winner); err != nil {
			return err
		}
	}
	series := book.seriesRefs()
	primary := SeriesRef{}
	if len(series) > 0 {
		primary = series[0]
	}
	var seriesID any
	if primary.Title != "" {
		id, err := lookup(writer.series, primary.Title, writer.stmts.series)
		if err != nil {
			return fmt.Errorf("series %q: %w", primary.Title, err)
		}
		seriesID = id
	}
	var seriesNum any
	if primary.SeqNumber > 0 {
		seriesNum = primary.SeqNumber
	}
	var year any
	if book.Year > 0 {
		year = book.Year
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE books SET lib_id=?, title=?, series_id=?, series_num=?, ext=?, size=?, lang=?, year=?, added=?, lib_rate=?, isbn=?, source_key=?, missing=0, deleted=0
		WHERE id=?`,
		book.LibID, book.Title, seriesID, seriesNum, book.Ext, book.Size, book.Lang, year, book.Added, book.Rate, NormalizeISBN(book.ISBN), key, winner); err != nil {
		return err
	}
	if err := writer.addBookRelations(winner, book, series); err != nil {
		return err
	}
	// The winner's stale search row is deleted after the publish loop (the
	// caller records it in polka_fts_dirty): per-row FTS5 deletes interleaved
	// with relation writes are the dominant publish cost at scale.
	if book.Deleted {
		if _, err := tx.ExecContext(ctx, `UPDATE books SET missing=1 WHERE id=?`, winner); err != nil {
			return err
		}
		return nil
	}
	// The search row is rebuilt in one bulk pass after the publish loop:
	// per-row restores would dominate a mass consolidation.
	return nil
}
