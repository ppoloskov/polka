package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vestigiumincaligne/polka/internal/store"
)

var folderBookExtensions = map[string]bool{
	"fb2": true, "epub": true, "pdf": true, "djvu": true, "txt": true, "mobi": true, "azw3": true,
}

// FileSourceKey identifies the exact path and bytes observed by a folder scan.
func FileSourceKey(relative, fileHash string) string {
	return "folder:v1:" + filepath.ToSlash(relative) + ":" + fileHash
}

// StageFolder builds a complete snapshot from supported regular files below
// root. It reads the source tree without modifying it; the caller owns the
// returned snapshot.
func StageFolder(ctx context.Context, root, stagingDir string, onProgress Progress) (_ *store.Snapshot, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if root == "" {
		return nil, errors.New("folder root is required")
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve folder root: %w", err)
	}
	rootPath, err = filepath.EvalSymlinks(filepath.Clean(rootPath))
	if err != nil {
		return nil, fmt.Errorf("resolve folder root: %w", err)
	}
	info, err := os.Stat(rootPath)
	if err != nil {
		return nil, fmt.Errorf("stat folder root: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("folder root must be a directory")
	}
	sourceRoot, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open folder root: %w", err)
	}
	defer sourceRoot.Close()

	if stagingDir == "" {
		stagingDir = os.TempDir()
	}
	stagePath, err := filepath.Abs(stagingDir)
	if err != nil {
		return nil, fmt.Errorf("resolve folder staging directory: %w", err)
	}
	stagePath, err = filepath.EvalSymlinks(filepath.Clean(stagePath))
	if err != nil {
		return nil, fmt.Errorf("resolve folder staging directory: %w", err)
	}
	if pathWithin(rootPath, stagePath) {
		return nil, errors.New("folder staging directory must be outside the source root")
	}
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
	err = fs.WalkDir(sourceRoot.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == "." || entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return nil
		}
		info, err := sourceRoot.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil
		}
		fileExt := strings.TrimPrefix(filepath.Ext(entry.Name()), ".")
		ext := strings.ToLower(fileExt)
		if !folderBookExtensions[ext] {
			return nil
		}
		before := info
		f, err := sourceRoot.Open(path)
		if err != nil {
			return err
		}
		opened, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		if !os.SameFile(before, opened) {
			f.Close()
			return fmt.Errorf("source file changed while opening: %s", path)
		}
		if err := ctx.Err(); err != nil {
			f.Close()
			return err
		}
		digest := sha256.New()
		bytesRead, copyErr := io.Copy(digest, f)
		if copyErr != nil {
			f.Close()
			return copyErr
		}
		if err := ctx.Err(); err != nil {
			f.Close()
			return err
		}
		relative := filepath.ToSlash(path)
		base := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		book, _ := BookFromOpenedFile(f, base, ext)
		after, statErr := f.Stat()
		closeErr := f.Close()
		if statErr != nil {
			return statErr
		}
		if closeErr != nil {
			return closeErr
		}
		pathAfter, pathErr := sourceRoot.Stat(path)
		if pathErr != nil {
			return pathErr
		}
		if !sameSourceStat(before, opened, after, pathAfter, bytesRead) {
			return fmt.Errorf("source file changed while staging: %s", relative)
		}
		book.Folder = filepath.ToSlash(filepath.Dir(relative))
		if book.Folder == "" {
			book.Folder = "."
		}
		fileHash := hex.EncodeToString(digest.Sum(nil))
		book.File, book.Ext, book.Size = base, fileExt, bytesRead
		book.FileHash, book.Added = fileHash, time.Now().Format("2006-01-02")
		key := FileSourceKey(relative, fileHash)
		if err := snapshot.Add(ctx, key, book); err != nil {
			return fmt.Errorf("stage folder file %q: %w", relative, err)
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if onProgress != nil {
		onProgress("loading", processed)
	}
	return snapshot, nil
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}

func sameSourceStat(before, opened, after, pathAfter os.FileInfo, bytesRead int64) bool {
	return os.SameFile(before, opened) && os.SameFile(before, after) && os.SameFile(before, pathAfter) &&
		before.Size() == opened.Size() && before.Size() == after.Size() && before.Size() == pathAfter.Size() &&
		before.ModTime().Equal(opened.ModTime()) && before.ModTime().Equal(after.ModTime()) &&
		before.ModTime().Equal(pathAfter.ModTime()) && bytesRead == before.Size()
}
