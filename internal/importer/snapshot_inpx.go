package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/vestigiumincaligne/polka/internal/inpx"
	"github.com/vestigiumincaligne/polka/internal/store"
)

// StageInpx builds a disk-backed snapshot from an INPX source. It copies the
// index into a temporary file before parsing so every staged row comes from
// the same owned input bytes. The caller owns the returned snapshot.
func StageInpx(ctx context.Context, path, stagingDir string, onProgress Progress) (_ *store.Snapshot, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if stagingDir != "" {
		if err := os.MkdirAll(stagingDir, 0700); err != nil {
			return nil, err
		}
	}
	copyFile, err := os.CreateTemp(stagingDir, "polka-inpx-*.inpx")
	if err != nil {
		return nil, fmt.Errorf("create INPX staging copy: %w", err)
	}
	copyPath := copyFile.Name()
	defer os.Remove(copyPath)
	defer copyFile.Close()

	source, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open INPX source: %w", err)
	}
	defer source.Close()
	before, err := source.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat INPX source: %w", err)
	}
	bytesCopied, err := io.Copy(copyFile, source)
	if err != nil {
		return nil, fmt.Errorf("copy INPX source: %w", err)
	}
	if err := copyFile.Close(); err != nil {
		return nil, fmt.Errorf("close INPX staging copy: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	after, statErr := source.Stat()
	pathAfter, pathStatErr := os.Stat(path)
	if statErr != nil {
		return nil, fmt.Errorf("restat INPX source: %w", statErr)
	}
	if pathStatErr != nil {
		return nil, fmt.Errorf("restat INPX path: %w", pathStatErr)
	}
	if !os.SameFile(before, after) || !os.SameFile(before, pathAfter) ||
		before.Size() != after.Size() || before.Size() != pathAfter.Size() ||
		!before.ModTime().Equal(after.ModTime()) || !before.ModTime().Equal(pathAfter.ModTime()) ||
		bytesCopied != before.Size() {
		return nil, fmt.Errorf("INPX source changed while it was being copied")
	}

	f, err := inpx.Open(copyPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	snapshot, err := store.NewSnapshot(stagingDir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			_ = snapshot.Close()
		}
	}()

	processed := 0
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
		processed++
		if onProgress != nil && processed%5_000 == 0 {
			onProgress("loading", processed)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if onProgress != nil {
		onProgress("loading", processed)
	}
	return snapshot, nil
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
