package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/vestigiumincaligne/polka/internal/library"
)

var errLibraryDirNotConfigured = errors.New("library dir is not configured")

func (s *Server) bookLibrary(ctx context.Context, bookID int64) (*library.Library, error) {
	owner, err := s.st.LibraryForBook(ctx, bookID)
	if err != nil {
		return nil, err
	}
	if owner.ID == "legacy" {
		if s.lib == nil {
			return nil, errLibraryDirNotConfigured
		}
		return s.lib, nil
	}
	if owner.Root == "" {
		return nil, library.ErrNoFile
	}
	if cached, ok := s.bookLibs.Load(owner.Root); ok {
		return cached.(*library.Library), nil
	}
	lib := library.New(owner.Root)
	cached, _ := s.bookLibs.LoadOrStore(owner.Root, lib)
	return cached.(*library.Library), nil
}

func (s *Server) bookLibraryOr404(w http.ResponseWriter, r *http.Request, bookID int64) *library.Library {
	lib, err := s.bookLibrary(r.Context(), bookID)
	if err != nil {
		s.apiError(w, err)
		return nil
	}
	return lib
}
