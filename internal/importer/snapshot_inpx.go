package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
		key, err := inpxSourceKey(book)
		if err != nil {
			return err
		}
		if err := snapshot.Add(ctx, key, book); err != nil {
			return fmt.Errorf("stage INPX record %q: %w", key, err)
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
	if onProgress != nil {
		onProgress("loading", processed)
	}
	info.CollectionName = f.CollectionName()
	info.Stats = store.ImportStats{Books: processed, Authors: len(authors), Series: len(series), Genres: len(genres), Duration: time.Since(started)}
	return snapshot, info, nil
}

func inpxSourceKey(book *store.BookInput) (string, error) {
	identity := *book
	identity.LibraryID = ""
	identity.SourceKey = ""
	identity.Deleted = false
	data, err := json.Marshal(&identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "inpx:v1:" + hex.EncodeToString(sum[:]), nil
}
