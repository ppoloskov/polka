// Package importer connects catalog sources (inpx, folder scanning)
// to the storage layer.
package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vestigiumincaligne/polka/internal/inpx"
	"github.com/vestigiumincaligne/polka/internal/store"
)

// Progress reports import progress: phase is "loading" or "indexing".
type Progress func(phase string, processed int)

// ImportInpx preserves the legacy import API while applying the source as a
// complete replacement of the default library.
func ImportInpx(ctx context.Context, log *slog.Logger, st *store.Store, path string, onProgress Progress) (store.ImportStats, error) {
	count, err := st.LibraryBookCount(ctx, "legacy")
	if err != nil {
		return store.ImportStats{}, err
	}
	if count > 0 {
		return store.ImportStats{}, fmt.Errorf("library is not empty (%d books); use --replace to reimport", count)
	}
	_, stats, err := UpdateInpx(ctx, log, st, "legacy", path, "", "", onProgress, false)
	return stats, err
}

// UpdateInpx stages and atomically publishes one INPX source into libraryID.
// A non-empty sourceName identifies an uploaded file; its verified bytes are
// retained under a content-addressed name before catalog publication.
func UpdateInpx(ctx context.Context, log *slog.Logger, st *store.Store, libraryID, path, dataDir, sourceName string, onProgress Progress, allowEmpty bool) (store.UpdateStats, store.ImportStats, error) {
	started := time.Now()
	snapshot, info, err := StageInpxWithInfo(ctx, path, dataDir, onProgress)
	if err != nil {
		return store.UpdateStats{}, store.ImportStats{}, err
	}
	defer snapshot.Close()
	if info.Stats.Books == 0 && !allowEmpty {
		return store.UpdateStats{}, store.ImportStats{}, errors.New("empty library snapshot is not allowed")
	}
	filenameSource := sourceName
	if filenameSource == "" {
		filenameSource = path
	}
	filename := filepath.Base(strings.ReplaceAll(filenameSource, "\\", "/"))
	sourcePath, err := filepath.Abs(path)
	if err != nil {
		return store.UpdateStats{}, store.ImportStats{}, err
	}
	if sourceName != "" {
		sourcePath, err = saveInpxSource(path, dataDir, info.SHA256)
		if err != nil {
			return store.UpdateStats{}, store.ImportStats{}, err
		}
	}
	snapshot.SetInpxSource(filename, info.SHA256, info.CollectionName, sourcePath, libraryID == "legacy")
	if info.CollectionName != "" && log != nil {
		log.Info("collection", "name", info.CollectionName)
	}
	if onProgress != nil {
		onProgress("indexing", info.Stats.Books)
	}
	updated, err := st.PublishLibrary(ctx, libraryID, snapshot, allowEmpty)
	if err != nil {
		return store.UpdateStats{}, store.ImportStats{}, err
	}
	info.Stats.Duration = time.Since(started)
	if log != nil {
		log.Info("import done", "books", info.Stats.Books, "duration", info.Stats.Duration.Round(time.Millisecond))
	}
	return updated, info.Stats, nil
}

func saveInpxSource(path, dataDir, expectedHash string) (string, error) {
	absoluteDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(absoluteDataDir, "sources")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".inpx-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	input, err := os.Open(path)
	if err != nil {
		tmp.Close()
		return "", err
	}
	digest := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(tmp, digest), input)
	closeInputErr := input.Close()
	if copyErr == nil {
		copyErr = closeInputErr
	}
	if copyErr != nil {
		tmp.Close()
		return "", copyErr
	}
	if got := hex.EncodeToString(digest.Sum(nil)); got != expectedHash {
		tmp.Close()
		return "", fmt.Errorf("uploaded INPX changed after staging")
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	destination := filepath.Join(dir, expectedHash+".inpx")
	verifyExisting := func() error {
		existing, err := os.Open(destination)
		if err != nil {
			return err
		}
		existingHash := sha256.New()
		_, copyErr := io.Copy(existingHash, existing)
		closeErr := existing.Close()
		if copyErr == nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			return copyErr
		}
		if hex.EncodeToString(existingHash.Sum(nil)) != expectedHash {
			return fmt.Errorf("content-addressed INPX source is invalid: %s", destination)
		}
		return nil
	}
	if _, err := os.Stat(destination); err == nil {
		if err := verifyExisting(); err != nil {
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	} else if err := os.Rename(tmpPath, destination); err != nil {
		if _, statErr := os.Stat(destination); statErr != nil {
			return "", err
		}
		if verifyErr := verifyExisting(); verifyErr != nil {
			return "", verifyErr
		}
	}
	if runtime.GOOS == "windows" {
		return destination, nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return "", err
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if syncErr != nil {
		return "", syncErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return destination, nil
}

func toBookInput(rec *inpx.Record) *store.BookInput {
	b := &store.BookInput{
		LibID:     rec.LibID,
		Title:     rec.Title,
		Series:    rec.Series,
		SeriesNum: rec.SeriesNum,
		Genres:    rec.Genres,
		Keywords:  rec.Keywords,
		Folder:    rec.Folder,
		File:      rec.File,
		Ext:       rec.Ext,
		Size:      rec.Size,
		Lang:      rec.Lang,
		Year:      rec.Year,
		Added:     rec.Date,
		Rate:      rec.Rate,
		Deleted:   rec.Deleted,
	}
	for _, a := range rec.Authors {
		b.Authors = append(b.Authors, store.AuthorName{Last: a.Last, First: a.First, Middle: a.Middle})
	}
	return b
}
