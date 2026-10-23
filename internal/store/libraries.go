package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

const bookIDHighWaterKey = "book_id_high_water"

type Library struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Root       string `json:"root"`
	SourcePath string `json:"sourcePath"`
	Enabled    bool   `json:"enabled"`
}

func (s *Store) ListLibraries(ctx context.Context) ([]Library, error) {
	rows, err := s.readDB.QueryContext(ctx, `
		SELECT id, name, kind, root, source_path, enabled FROM libraries
		ORDER BY CASE WHEN id = 'legacy' THEN 0 ELSE 1 END, name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var libraries []Library
	for rows.Next() {
		var l Library
		var enabled int
		if err := rows.Scan(&l.ID, &l.Name, &l.Kind, &l.Root, &l.SourcePath, &enabled); err != nil {
			return nil, err
		}
		l.Enabled = enabled != 0
		libraries = append(libraries, l)
	}
	return libraries, rows.Err()
}

func (s *Store) Library(ctx context.Context, id string) (*Library, error) {
	var l Library
	var enabled int
	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, name, kind, root, source_path, enabled FROM libraries WHERE id = ?`, id).
		Scan(&l.ID, &l.Name, &l.Kind, &l.Root, &l.SourcePath, &enabled)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l.Enabled = enabled != 0
	return &l, nil
}

func (s *Store) AddLibrary(ctx context.Context, l Library) (*Library, error) {
	l.Name = strings.TrimSpace(l.Name)
	if l.Name == "" {
		return nil, fmt.Errorf("library name is required")
	}
	if l.Kind != "inpx" && l.Kind != "folder" {
		return nil, fmt.Errorf("unsupported library kind %q", l.Kind)
	}
	if l.Root == "" || !filepath.IsAbs(l.Root) {
		return nil, fmt.Errorf("library root must be an absolute path")
	}
	if l.SourcePath != "" && !filepath.IsAbs(l.SourcePath) {
		return nil, fmt.Errorf("library source path must be absolute")
	}
	if l.Kind == "inpx" && l.SourcePath == "" {
		return nil, fmt.Errorf("INPX source path is required")
	}
	l.ID = rand.Text()
	l.Enabled = true
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO libraries (id, name, kind, root, source_path, enabled) VALUES (?, ?, ?, ?, ?, ?)`,
		l.ID, l.Name, l.Kind, l.Root, l.SourcePath, boolToInt(l.Enabled))
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// UpdateLibrary changes a library's display name and/or availability. Source
// paths and kind are fixed when a library is created.
func (s *Store) UpdateLibrary(ctx context.Context, id string, name *string, enabled *bool) error {
	if name == nil && enabled == nil {
		return fmt.Errorf("library update is empty")
	}
	var nameValue any
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if trimmed == "" {
			return fmt.Errorf("library name is required")
		}
		nameValue = trimmed
	}
	var enabledValue any
	if enabled != nil {
		enabledValue = boolToInt(*enabled)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE libraries
		SET name=COALESCE(?, name), enabled=COALESCE(?, enabled)
		WHERE id=?`, nameValue, enabledValue, id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrNotFound
	}
	s.invalidateBookCount()
	return nil
}

// ReserveBookIDs raises the durable ID floor. It is safe to call repeatedly.
func (s *Store) ReserveBookIDs(ctx context.Context, maxID int64) error {
	if maxID < 0 {
		return fmt.Errorf("book ID floor cannot be negative")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = CAST(max(
			CAST(meta.value AS INTEGER), CAST(excluded.value AS INTEGER)
		) AS TEXT)`, bookIDHighWaterKey, maxID)
	return err
}

func allocateBookID(ctx context.Context, tx *sql.Tx) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		UPDATE meta SET value = CAST(CAST(value AS INTEGER) + 1 AS TEXT)
		WHERE key = ? RETURNING CAST(value AS INTEGER)`, bookIDHighWaterKey).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("allocate book ID: %w", err)
	}
	return id, nil
}
