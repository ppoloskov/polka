package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/vestigiumincaligne/polka/internal/inpx"
	"github.com/vestigiumincaligne/polka/internal/store"
)

// StageInpx builds a disk-backed snapshot from an INPX source. It copies the
// index into a temporary file before parsing so every staged row comes from
// the same owned input bytes. The caller owns the returned snapshot.
func StageInpx(ctx context.Context, path, stagingDir string, onProgress Progress) (_ *store.Snapshot, resultErr error) {
	snapshot, _, err := StageInpxWithInfo(ctx, path, stagingDir, onProgress)
	return snapshot, err
}

// InpxInfo describes the source and compatibility counters parsed while
// staging, without retaining the catalog in memory.
type InpxInfo struct {
	SHA256         string
	CollectionName string
	Stats          store.ImportStats
}

// StageInpxWithInfo stages records and returns metadata from the exact copied
// input bytes that were parsed.
func StageInpxWithInfo(ctx context.Context, path, stagingDir string, onProgress Progress) (_ *store.Snapshot, info InpxInfo, resultErr error) {
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return nil, info, err
	}
	if stagingDir != "" {
		if err := os.MkdirAll(stagingDir, 0700); err != nil {
			return nil, info, err
		}
	}
	copyFile, err := os.CreateTemp(stagingDir, "polka-inpx-*.inpx")
	if err != nil {
		return nil, info, fmt.Errorf("create INPX staging copy: %w", err)
	}
	copyPath := copyFile.Name()
	defer os.Remove(copyPath)
	defer copyFile.Close()

	source, err := os.Open(path)
	if err != nil {
		return nil, info, fmt.Errorf("open INPX source: %w", err)
	}
	defer source.Close()
	before, err := source.Stat()
	if err != nil {
		return nil, info, fmt.Errorf("stat INPX source: %w", err)
	}
	digest := sha256.New()
	bytesCopied, err := io.Copy(io.MultiWriter(copyFile, digest), source)
	if err != nil {
		return nil, info, fmt.Errorf("copy INPX source: %w", err)
	}
	if err := copyFile.Close(); err != nil {
		return nil, info, fmt.Errorf("close INPX staging copy: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, info, err
	}
	after, statErr := source.Stat()
	pathAfter, pathStatErr := os.Stat(path)
	if statErr != nil {
		return nil, info, fmt.Errorf("restat INPX source: %w", statErr)
	}
	if pathStatErr != nil {
		return nil, info, fmt.Errorf("restat INPX path: %w", pathStatErr)
	}
	if !os.SameFile(before, after) || !os.SameFile(before, pathAfter) ||
		before.Size() != after.Size() || before.Size() != pathAfter.Size() ||
		!before.ModTime().Equal(after.ModTime()) || !before.ModTime().Equal(pathAfter.ModTime()) ||
		bytesCopied != before.Size() {
		return nil, info, fmt.Errorf("INPX source changed while it was being copied")
	}
	info.SHA256 = hex.EncodeToString(digest.Sum(nil))

	f, err := inpx.Open(copyPath)
	if err != nil {
		return nil, info, err
	}
	defer f.Close()

	snapshot, err := store.NewSnapshot(stagingDir)
	if err != nil {
		return nil, info, err
	}
	defer func() {
		if resultErr != nil {
			_ = snapshot.Close()
		}
	}()

	processed := 0
	authors, series, genres := map[store.AuthorName]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	err = f.RecordsStrict(func(rec *inpx.Record) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		book := toBookInput(rec)
		hash, err := inpxLineHash(book)
		if err != nil {
			return err
		}
		fileKey := book.Folder + "\x00" + book.File + "\x00" + book.Ext
		if err := snapshot.AddInpxLine(ctx, fileKey, hash, *book); err != nil {
			return fmt.Errorf("stage INPX record %q: %w", fileKey, err)
		}
		for _, author := range book.Authors {
			authors[author] = struct{}{}
		}
		if book.Series != "" {
			series[book.Series] = struct{}{}
		}
		for _, ref := range book.SeriesList {
			if ref.Title != "" {
				series[ref.Title] = struct{}{}
			}
		}
		for _, genre := range book.Genres {
			genres[genre] = struct{}{}
		}
		processed++
		if onProgress != nil && processed%5_000 == 0 {
			onProgress("loading", processed)
		}
		return nil
	})
	if err != nil {
		return nil, info, err
	}
	groups, err := snapshot.CollapseInpxLines(ctx, mergeInpxLines)
	if err != nil {
		return nil, info, err
	}
	if onProgress != nil {
		onProgress("loading", processed)
	}
	info.CollectionName = f.CollectionName()
	info.Stats = store.ImportStats{Books: groups, Authors: len(authors), Series: len(series), Genres: len(genres), Duration: time.Since(started)}
	return snapshot, info, nil
}

// inpxV2KeyPrefix prefixes group fingerprints: one staged entry per file,
// however many per-series lines the index repeats for it.
const inpxV2KeyPrefix = "inpx:v2:"

// inpxLineHash is the per-line content hash (the v1 fingerprint without its
// prefix): the record JSON with runtime and deletion fields zeroed.
func inpxLineHash(book *store.BookInput) (string, error) {
	identity := *book
	identity.LibraryID = ""
	identity.SourceKey = ""
	identity.Deleted = false
	data, err := json.Marshal(&identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// mergeInpxLines folds the per-series lines of one file into a single staged
// group. The first line in index order supplies the base fields; authors,
// genres and keywords are unions in first-seen order; every line's series is
// kept in line order. The group fingerprint covers every sorted member hash,
// so any metadata change in any member removes and re-adds the entry.
func mergeInpxLines(members []store.StagedLine) (string, store.BookInput, error) {
	book := members[0].Book
	book.Deleted = true
	var authors []store.AuthorName
	var genres, keywords []string
	var seriesList []store.SeriesRef
	seenAuthors := make(map[store.AuthorName]struct{})
	seenGenres := make(map[string]struct{})
	seenKeywords := make(map[string]struct{})
	hashes := make([]string, 0, len(members))
	for _, member := range members {
		hashes = append(hashes, member.Hash)
		if !member.Book.Deleted {
			book.Deleted = false
		}
		for _, author := range member.Book.Authors {
			if _, ok := seenAuthors[author]; !ok {
				seenAuthors[author] = struct{}{}
				authors = append(authors, author)
			}
		}
		for _, genre := range member.Book.Genres {
			if _, ok := seenGenres[genre]; !ok {
				seenGenres[genre] = struct{}{}
				genres = append(genres, genre)
			}
		}
		for _, keyword := range member.Book.Keywords {
			if _, ok := seenKeywords[keyword]; !ok {
				seenKeywords[keyword] = struct{}{}
				keywords = append(keywords, keyword)
			}
		}
		if member.Book.Series != "" {
			seriesList = append(seriesList, store.SeriesRef{Title: member.Book.Series, SeqNumber: member.Book.SeriesNum})
		}
	}
	book.Authors = authors
	book.Genres = genres
	book.Keywords = keywords
	book.SeriesList = seriesList
	sort.Strings(hashes)
	sum := sha256.Sum256([]byte(strings.Join(hashes, "")))
	return inpxV2KeyPrefix + hex.EncodeToString(sum[:]), book, nil
}
