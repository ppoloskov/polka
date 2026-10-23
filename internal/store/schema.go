package store

// The schema version is stored in PRAGMA user_version.
const schemaVersion = 6

// v6: persistent library ownership and a monotonic public book-ID floor.
const schemaV6 = `
CREATE TABLE IF NOT EXISTS libraries (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL,
	kind        TEXT NOT NULL,
	root        TEXT NOT NULL DEFAULT '',
	source_path TEXT NOT NULL DEFAULT '',
	enabled     INTEGER NOT NULL DEFAULT 1
);
ALTER TABLE books ADD COLUMN library_id TEXT NOT NULL DEFAULT 'legacy';
ALTER TABLE books ADD COLUMN missing INTEGER NOT NULL DEFAULT 0;
ALTER TABLE books ADD COLUMN source_key TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_books_library_source ON books (library_id, source_key);
INSERT INTO libraries (id, name, kind, root, source_path, enabled)
SELECT 'legacy', coalesce((SELECT value FROM meta WHERE key = 'collection_name' AND trim(value) != ''), 'Library'), 'legacy', '', '', 1
WHERE NOT EXISTS (SELECT 1 FROM libraries WHERE id = 'legacy');
INSERT INTO meta (key, value)
VALUES ('book_id_high_water', CAST(max(
	coalesce((SELECT max(id) FROM books), 0),
	coalesce((SELECT max(book_id) FROM book_digests), 0)
) AS TEXT))
ON CONFLICT (key) DO UPDATE SET value = CAST(max(
	CAST(meta.value AS INTEGER), CAST(excluded.value AS INTEGER)
) AS TEXT);
`

// v5: a book may belong to several series. Keep books.series_id/series_num
// as the primary series for existing cards and recommendations.
const schemaV5 = `
CREATE TABLE IF NOT EXISTS book_series (
	book_id    INTEGER NOT NULL,
	series_id  INTEGER NOT NULL,
	series_num INTEGER NOT NULL DEFAULT 0,
	position   INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (book_id, series_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_book_series_series ON book_series (series_id, book_id);
INSERT OR IGNORE INTO book_series (book_id, series_id, series_num)
SELECT id, series_id, coalesce(series_num, 0) FROM books WHERE series_id IS NOT NULL;
`

// v4: KOReader partial-MD5 digests of served book files — the bridge
// between kosync documents and catalog books. Recomputed lazily (on
// download), so a re-import simply starts collecting them again.
const schemaV4 = `
CREATE TABLE book_digests (
	digest  TEXT PRIMARY KEY,
	book_id INTEGER NOT NULL
) WITHOUT ROWID;
`

// v3: ISBN for search. Populated on web upload and lazily —
// on the first view of the book card (from the file's publish-info).
const schemaV3 = `
ALTER TABLE books ADD COLUMN isbn TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_books_isbn ON books (isbn) WHERE isbn != '';
`

// v2 catches up databases created before duplicate hashes existed.
// Fresh databases get these columns right away in schemaV1.
const schemaV2 = `
ALTER TABLE books ADD COLUMN file_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN content_hash TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_books_file_hash ON books (file_hash) WHERE file_hash != '';
CREATE INDEX IF NOT EXISTS idx_books_content_hash ON books (content_hash) WHERE content_hash != '';
`

const schemaV1 = `
CREATE TABLE meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
) WITHOUT ROWID;

CREATE TABLE folders (
	id   INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE
);

CREATE TABLE series (
	id    INTEGER PRIMARY KEY,
	title TEXT NOT NULL UNIQUE
);

CREATE TABLE authors (
	id          INTEGER PRIMARY KEY,
	last_name   TEXT NOT NULL DEFAULT '',
	first_name  TEXT NOT NULL DEFAULT '',
	middle_name TEXT NOT NULL DEFAULT '',
	UNIQUE (last_name, first_name, middle_name)
);

CREATE TABLE genres (
	id   INTEGER PRIMARY KEY,
	code TEXT NOT NULL UNIQUE,
	name TEXT NOT NULL DEFAULT ''
);

CREATE TABLE keywords (
	id   INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE
);

CREATE TABLE books (
	id         INTEGER PRIMARY KEY,
	lib_id     TEXT NOT NULL DEFAULT '',
	title      TEXT NOT NULL,
	series_id  INTEGER,
	series_num INTEGER,
	folder_id  INTEGER NOT NULL REFERENCES folders (id),
	file       TEXT NOT NULL,
	ext        TEXT NOT NULL DEFAULT 'fb2',
	size       INTEGER NOT NULL DEFAULT 0,
	lang       TEXT NOT NULL DEFAULT '',
	year       INTEGER,
	added      TEXT NOT NULL DEFAULT '',
	lib_rate   REAL NOT NULL DEFAULT 0,
	deleted    INTEGER NOT NULL DEFAULT 0,
	file_hash    TEXT NOT NULL DEFAULT '',
	content_hash TEXT NOT NULL DEFAULT '',
	isbn         TEXT NOT NULL DEFAULT ''
);

CREATE TABLE book_series (
	book_id    INTEGER NOT NULL,
	series_id  INTEGER NOT NULL,
	series_num INTEGER NOT NULL DEFAULT 0,
	position   INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (book_id, series_id)
) WITHOUT ROWID;

CREATE TABLE book_authors (
	book_id   INTEGER NOT NULL,
	author_id INTEGER NOT NULL,
	PRIMARY KEY (book_id, author_id)
) WITHOUT ROWID;

CREATE TABLE book_genres (
	book_id  INTEGER NOT NULL,
	genre_id INTEGER NOT NULL,
	PRIMARY KEY (book_id, genre_id)
) WITHOUT ROWID;

CREATE TABLE book_keywords (
	book_id    INTEGER NOT NULL,
	keyword_id INTEGER NOT NULL,
	PRIMARY KEY (book_id, keyword_id)
) WITHOUT ROWID;

-- KOReader partial-MD5 digests of served files (see schemaV4).
CREATE TABLE book_digests (
	digest  TEXT PRIMARY KEY,
	book_id INTEGER NOT NULL
) WITHOUT ROWID;

CREATE VIRTUAL TABLE book_search USING fts5(
	title,
	authors,
	series,
	tokenize = 'unicode61 remove_diacritics 2'
);

CREATE VIRTUAL TABLE authors_search USING fts5(
	name,
	tokenize = 'unicode61 remove_diacritics 2'
);

CREATE VIRTUAL TABLE series_search USING fts5(
	title,
	tokenize = 'unicode61 remove_diacritics 2'
);
`

// Indexes are kept separate: during bulk import it is cheaper to
// build them after the data is loaded than to maintain them on every insert.
var schemaIndexes = []string{
	`CREATE INDEX IF NOT EXISTS idx_book_series_series ON book_series (series_id, book_id)`,
	`CREATE INDEX IF NOT EXISTS idx_books_series ON books (series_id) WHERE series_id IS NOT NULL`,
	`CREATE INDEX IF NOT EXISTS idx_books_folder ON books (folder_id)`,
	// (added, id) and (lib_rate, id) serve shelf ORDER BYs: a reverse index
	// scan yields a DESC selection with LIMIT without a full sort.
	`CREATE INDEX IF NOT EXISTS idx_books_added ON books (added, id) WHERE deleted = 0`,
	`CREATE INDEX IF NOT EXISTS idx_books_rate ON books (lib_rate, id) WHERE deleted = 0`,
	`CREATE INDEX IF NOT EXISTS idx_book_authors_author ON book_authors (author_id, book_id)`,
	`CREATE INDEX IF NOT EXISTS idx_book_genres_genre ON book_genres (genre_id, book_id)`,
	`CREATE INDEX IF NOT EXISTS idx_book_keywords_keyword ON book_keywords (keyword_id, book_id)`,
	`CREATE INDEX IF NOT EXISTS idx_authors_name ON authors (last_name, first_name)`,
	`CREATE INDEX IF NOT EXISTS idx_books_file_hash ON books (file_hash) WHERE file_hash != ''`,
	`CREATE INDEX IF NOT EXISTS idx_books_content_hash ON books (content_hash) WHERE content_hash != ''`,
	`CREATE INDEX IF NOT EXISTS idx_books_isbn ON books (isbn) WHERE isbn != ''`,
}
