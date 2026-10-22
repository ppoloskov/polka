package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/vestigiumincaligne/polka/internal/importer"
	"github.com/vestigiumincaligne/polka/internal/library"
	"github.com/vestigiumincaligne/polka/internal/store"
)

type libraryCreateRequest struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Root       string `json:"root"`
	SourcePath string `json:"sourcePath"`
}

type libraryUpdateRequest struct {
	Name    *string `json:"name"`
	Enabled *bool   `json:"enabled"`
}

type libraryUpdateStartRequest struct {
	AllowEmpty bool `json:"allowEmpty"`
}

type publicLibrary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func decodeLibraryRequest(w http.ResponseWriter, r *http.Request, dest any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	return nil
}

func (s *Server) handleAdminLibraries(w http.ResponseWriter, r *http.Request) {
	libraries, err := s.st.ListLibraries(r.Context())
	if err != nil {
		s.apiError(w, err)
		return
	}
	writeJSON(w, map[string]any{"libraries": libraries})
}

func (s *Server) handlePublicLibraries(w http.ResponseWriter, r *http.Request) {
	libraries, err := s.st.ListLibraries(r.Context())
	if err != nil {
		s.apiError(w, err)
		return
	}
	out := make([]publicLibrary, 0, len(libraries))
	for _, item := range libraries {
		if item.Enabled {
			out = append(out, publicLibrary{ID: item.ID, Name: item.Name, Kind: item.Kind})
		}
	}
	writeJSON(w, map[string]any{"libraries": out})
}

func (s *Server) handleLibraryCreate(w http.ResponseWriter, r *http.Request) {
	var req libraryCreateRequest
	if err := decodeLibraryRequest(w, r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	item, err := s.st.AddLibrary(r.Context(), store.Library{
		Name: req.Name, Kind: req.Kind, Root: req.Root, SourcePath: req.SourcePath,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"library": item})
}

func (s *Server) handleLibraryUpdate(w http.ResponseWriter, r *http.Request) {
	var req libraryUpdateRequest
	if err := decodeLibraryRequest(w, r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == nil && req.Enabled == nil {
		http.Error(w, "library update is empty", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	if err := s.st.UpdateLibrary(r.Context(), id, req.Name, req.Enabled); err != nil {
		if err == store.ErrNotFound {
			http.NotFound(w, r)
		} else {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	if req.Enabled != nil {
		s.invalidateGenreCounts()
	}
	item, err := s.st.Library(r.Context(), id)
	if err != nil {
		s.apiError(w, err)
		return
	}
	writeJSON(w, map[string]any{"library": item})
}

func (s *Server) handleLibraryUpdateStart(w http.ResponseWriter, r *http.Request) {
	var req libraryUpdateStartRequest
	if err := decodeLibraryRequest(w, r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	item, err := s.st.Library(r.Context(), r.PathValue("id"))
	if err != nil {
		if err == store.ErrNotFound {
			http.NotFound(w, r)
		} else {
			s.apiError(w, err)
		}
		return
	}
	if item.Kind == "legacy" && item.SourcePath == "" {
		http.Error(w, "legacy library has no saved INPX source path; use the existing import workflow first", http.StatusBadRequest)
		return
	}
	switch item.Kind {
	case "legacy", "inpx", "folder":
	default:
		http.Error(w, "unsupported library kind", http.StatusBadRequest)
		return
	}

	if !s.startImport(item.ID) {
		http.Error(w, "import is already running", http.StatusConflict)
		return
	}

	go s.runLibraryUpdate(item, req.AllowEmpty)
	writeJSON(w, map[string]any{"started": true})
}

func (s *Server) runLibraryUpdate(item *store.Library, allowEmpty bool) {
	ctx := context.Background()
	progress := s.importProgress()
	var stats store.ImportStats
	var update store.UpdateStats
	var updateErr error
	switch item.Kind {
	case "legacy", "inpx":
		update, stats, updateErr = importer.UpdateInpx(ctx, s.log, s.st, item.ID, item.SourcePath, s.cfg.DataDir, "", progress, allowEmpty)
	case "folder":
		var snapshot *store.Snapshot
		snapshot, updateErr = importer.StageFolder(ctx, item.Root, s.cfg.DataDir, progress)
		if updateErr == nil {
			progress("indexing", 0)
			update, updateErr = s.st.PublishLibrary(ctx, item.ID, snapshot, allowEmpty)
			_ = snapshot.Close()
			stats.Books = update.Added + update.Changed + update.Unchanged
		}
	default:
		updateErr = fmt.Errorf("unsupported library kind %q", item.Kind)
	}
	if updateErr == nil {
		s.resetLibraryCovers(item)
		s.invalidateGenreCounts()
	}
	s.finishImport(update, stats, updateErr)
	if updateErr != nil {
		s.log.Error("library update", "library", item.ID, "error", updateErr)
	}
	if updateErr == nil && item.ID == "legacy" {
		s.warnIfArchivesMissing(ctx)
		s.rematchCollections(ctx)
	}
}

func (s *Server) invalidateGenreCounts() {
	s.genres.mu.Lock()
	s.genres.counts = nil
	s.genres.fetched = time.Time{}
	s.genres.mu.Unlock()
}

func (s *Server) resetLibraryCovers(item *store.Library) {
	if item.ID == "legacy" {
		if s.lib != nil {
			s.lib.ResetCovers()
		}
		return
	}
	if cached, ok := s.bookLibs.Load(item.Root); ok {
		cached.(*library.Library).ResetCovers()
	}
}
