package auth

import (
	"context"
	"errors"
)

// Reading bookmarks: named, per-book positions the reader can save and jump
// to. They live in users.db like all personal state.

const bookmarksSchema = `
CREATE TABLE IF NOT EXISTS bookmarks (
	id         INTEGER PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	book_id    INTEGER NOT NULL,
	chapter    INTEGER NOT NULL,
	position   REAL NOT NULL,
	locator    TEXT NOT NULL DEFAULT '',
	label      TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_bookmarks_user_book ON bookmarks (user_id, book_id);
`

// Bookmark is one saved position inside a book (chapter index and the
// within-chapter reading fraction).
type Bookmark struct {
	ID       int64   `json:"id"`
	Chapter  int     `json:"chapter"`
	Position float64 `json:"position"`
	Locator  string  `json:"locator,omitempty"`
	Label    string  `json:"label,omitempty"`
	Created  string  `json:"created,omitempty"`
}

// ErrBookmarkNotFound when the bookmark exists but belongs to another book
// or user.
var ErrBookmarkNotFound = errors.New("bookmark not found")

// Bookmarks lists the saved positions of one book, in reading order.
func (s *Service) Bookmarks(ctx context.Context, userID, bookID int64) ([]Bookmark, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, chapter, position, locator, label, updated_at
		FROM bookmarks WHERE user_id = ? AND book_id = ?
		ORDER BY chapter, position, id`, userID, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bookmark
	for rows.Next() {
		var b Bookmark
		if err := rows.Scan(&b.ID, &b.Chapter, &b.Position, &b.Locator, &b.Label, &b.Created); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// AddBookmark saves a position. Calling it twice at the same spot
// (same chapter and within-chapter position) updates the first entry
// instead of stacking duplicates.
func (s *Service) AddBookmark(ctx context.Context, userID, bookID int64, chapter int, position float64, locator, label string) (Bookmark, error) {
	b := Bookmark{Chapter: chapter, Position: position, Locator: locator, Label: label}
	if err := s.db.QueryRowContext(ctx, `
		SELECT id FROM bookmarks WHERE user_id = ? AND book_id = ? AND chapter = ? AND position > ? AND position < ?`,
		userID, bookID, chapter, position-0.01, position+0.01).Scan(&b.ID); err == nil {
		_, err := s.db.ExecContext(ctx, `
			UPDATE bookmarks SET label = ?, updated_at = datetime('now') WHERE id = ?`, label, b.ID)
		if err != nil {
			return b, err
		}
		return b, nil
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO bookmarks (user_id, book_id, chapter, position, locator, label) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, bookID, chapter, position, locator, label)
	if err != nil {
		return b, err
	}
	b.ID, _ = res.LastInsertId()
	return b, nil
}

// DeleteBookmark removes one bookmark, scoped to its owner.
func (s *Service) DeleteBookmark(ctx context.Context, userID, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM bookmarks WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBookmarkNotFound
	}
	return nil
}
