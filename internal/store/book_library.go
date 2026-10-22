package store

import (
	"context"
	"database/sql"
	"errors"
)

// LibraryForBook returns the configured root for a book's owning library.
func (s *Store) LibraryForBook(ctx context.Context, bookID int64) (*Library, error) {
	var l Library
	var enabled int
	err := s.db.QueryRowContext(ctx, `
		SELECT l.id, l.name, l.kind, l.root, l.source_path, l.enabled
		FROM books b JOIN libraries l ON l.id = b.library_id
		WHERE b.id = ? AND b.deleted = 0 AND b.missing = 0 AND l.enabled = 1`, bookID).
		Scan(&l.ID, &l.Name, &l.Kind, &l.Root, &l.SourcePath, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l.Enabled = enabled != 0
	return &l, nil
}
