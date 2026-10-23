// Package sqlitedrv selects the SQLite driver per platform:
// pure Go (modernc) everywhere, the CGO mattn driver on Android:
// modernc/libc uses raw system calls (lstat and others)
// that Android forbids via seccomp.
package sqlitedrv

import "database/sql"

// Options are common connection settings independent of the driver.
type Options struct {
	WAL         bool
	SyncNormal  bool
	BusyTimeout int // milliseconds
	ForeignKeys bool
	// CacheSizeKiB enlarges the per-connection page cache (SQLite defaults to
	// ~2 MiB, far too small for a catalog that outgrows RAM by orders of
	// magnitude). Zero keeps the SQLite default.
	CacheSizeKiB int
}

// Open opens the database with the settings, translating them into the driver's syntax.
func Open(path string, o Options) (*sql.DB, error) {
	return sql.Open(driverName, dsn(path, o))
}
