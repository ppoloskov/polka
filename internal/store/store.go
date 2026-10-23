// Package store is the storage layer: SQLite (WAL, FTS5) and operations on it.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/vestigiumincaligne/polka/internal/sqlitedrv"
)

type Store struct {
	db     *sql.DB
	readDB *sql.DB
}

func Open(path string) (*Store, error) {
	opts := sqlitedrv.Options{
		WAL: true, SyncNormal: true, BusyTimeout: 10000, ForeignKeys: true,
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
	// In-memory databases are connection-local unless opened with a shared
	// cache URI. Reuse the writer in that case; file databases get a WAL reader.
	if strings.Contains(path, ":memory:") {
		s.readDB = db
	} else {
		reader, err := sqlitedrv.Open(path, opts)
		if err != nil {
			db.Close()
			return nil, err
		}
		reader.SetMaxOpenConns(1)
		s.readDB = reader
	}
	return s, nil
}

func (s *Store) Close() error {
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
