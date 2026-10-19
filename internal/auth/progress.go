package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Reading progress lives in users.db: it is personal and must
// survive collection re-import.

const progressSchema = `
CREATE TABLE IF NOT EXISTS reading_progress (
	user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	book_id    INTEGER NOT NULL,
	chapter    INTEGER NOT NULL DEFAULT 0,
	position   REAL NOT NULL DEFAULT 0,
	overall    REAL NOT NULL DEFAULT 0,
	locator    TEXT NOT NULL DEFAULT '',
	revision   INTEGER NOT NULL DEFAULT 0,
	updated_at TEXT NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY (user_id, book_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_progress_updated ON reading_progress (user_id, updated_at);
`

// migrateProgress upgrades old user databases without losing their bookmarks.
func migrateProgress(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`PRAGMA table_info(reading_progress)`)
	if err != nil {
		return err
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, kind string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, column := range []struct{ name, definition string }{
		{"overall", "REAL NOT NULL DEFAULT 0"},
		{"locator", "TEXT NOT NULL DEFAULT ''"},
		{"revision", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if !columns[column.name] {
			if _, err := tx.Exec(`ALTER TABLE reading_progress ADD COLUMN ` + column.name + ` ` + column.definition); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

type Progress struct {
	Revision int64
	BookID   int64
	Chapter  int
	Position float64 // scroll fraction of the chapter, 0..1
	Overall  float64 // fraction of the whole book, 0..1
	Locator  string  // format-specific position (CFI for epub, etc.)
}

// SaveProgress stores the position with a millisecond timestamp: the
// sync merge is last-write-wins on updated_at, and with whole seconds two
// saves within the same second (e.g. right before and after going
// offline) would compare equal and the later one would be dropped.
func (s *Service) SaveProgress(ctx context.Context, userID, bookID int64, p Progress) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO reading_progress (user_id, book_id, chapter, position, overall, locator, revision, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 1, strftime('%Y-%m-%d %H:%M:%f', 'now'))
		ON CONFLICT (user_id, book_id) DO UPDATE
		SET chapter = excluded.chapter, position = excluded.position,
		    overall = excluded.overall, locator = excluded.locator, revision = reading_progress.revision + 1, updated_at = excluded.updated_at`,
		userID, bookID, p.Chapter, p.Position, p.Overall, p.Locator)
	return err
}

func (s *Service) GetProgress(ctx context.Context, userID, bookID int64) (Progress, error) {
	p := Progress{BookID: bookID}
	err := s.db.QueryRowContext(ctx,
		`SELECT chapter, position, overall, locator, revision FROM reading_progress WHERE user_id = ? AND book_id = ?`,
		userID, bookID).Scan(&p.Chapter, &p.Position, &p.Overall, &p.Locator, &p.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// ListProgress returns the user's recently read books (newest first).
// Finished books (overall ~1) are not shown.
func (s *Service) ListProgress(ctx context.Context, userID int64, limit int) ([]Progress, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT book_id, chapter, position, overall FROM reading_progress
		WHERE user_id = ? AND overall < 0.98
		ORDER BY updated_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Progress
	for rows.Next() {
		var p Progress
		if err := rows.Scan(&p.BookID, &p.Chapter, &p.Position, &p.Overall); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ErrProgressConflict means another reader updated the position since it was loaded.
var ErrProgressConflict = errors.New("reading position changed")

// SaveBridgedProgress atomically saves the web position and its KOReader form.
// A nil expected revision is used for protocol clients without revision support.
func (s *Service) SaveBridgedProgress(ctx context.Context, userID, bookID int64, p Progress, expected *int64, ko *KosyncProgress) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// The first statement takes the SQLite write lock before checking revisions.
	var revision int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO reading_progress (user_id, book_id, chapter, position, overall, locator, revision, updated_at)
		SELECT ?, ?, ?, ?, ?, ?, 1, strftime('%Y-%m-%d %H:%M:%f', 'now')
		WHERE ? IS NULL OR ? = 0 OR EXISTS (SELECT 1 FROM reading_progress WHERE user_id = ? AND book_id = ?)
		ON CONFLICT (user_id, book_id) DO UPDATE SET
			chapter = excluded.chapter, position = excluded.position, overall = excluded.overall,
			locator = excluded.locator, revision = reading_progress.revision + 1, updated_at = excluded.updated_at
		WHERE ? IS NULL OR reading_progress.revision = ?
		RETURNING revision`, userID, bookID, p.Chapter, p.Position, p.Overall, p.Locator, expected, expected, userID, bookID, expected, expected).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrProgressConflict
	}
	if err != nil {
		return 0, err
	}
	if ko != nil {
		ts := time.Now().Unix()
		_, err = tx.ExecContext(ctx, `
			INSERT INTO kosync_progress (user_id, document, progress, percentage, device, device_id, timestamp)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (user_id, document) DO UPDATE SET
				progress = excluded.progress, percentage = excluded.percentage,
				device = excluded.device, device_id = excluded.device_id, timestamp = excluded.timestamp`,
			userID, ko.Document, ko.Progress, ko.Percentage, ko.Device, ko.DeviceID, ts)
		if err != nil {
			return 0, fmt.Errorf("sync position: %w", err)
		}
	}
	return revision, tx.Commit()
}
