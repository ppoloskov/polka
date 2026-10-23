package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/vestigiumincaligne/polka/internal/library"
)

var errLibraryDirNotConfigured = errors.New("library dir is not configured")

func (s *Server) bookLibrary(ctx context.Context, bookID int64) (*library.Library, error) {
	lib, _, err := s.bookLibraryOwner(ctx, bookID)
	return lib, err
}

// bookLibraryOwner additionally reports the book's owning catalog library ID.
func (s *Server) bookLibraryOwner(ctx context.Context, bookID int64) (*library.Library, string, error) {
	owner, err := s.st.LibraryForBook(ctx, bookID)
	if err != nil {
		return nil, "", err
	}
	if owner.ID == "legacy" {
		if s.lib == nil {
			return nil, "", errLibraryDirNotConfigured
		}
		return s.lib, owner.ID, nil
	}
	if owner.Root == "" {
		return nil, "", library.ErrNoFile
	}
	if cached, ok := s.bookLibs.Load(owner.Root); ok {
		return cached.(*library.Library), owner.ID, nil
	}
	lib := library.New(owner.Root)
	cached, _ := s.bookLibs.LoadOrStore(owner.Root, lib)
	return cached.(*library.Library), owner.ID, nil
}

func (s *Server) bookLibraryOr404(w http.ResponseWriter, r *http.Request, bookID int64) *library.Library {
	lib, err := s.bookLibrary(r.Context(), bookID)
	if err != nil {
		s.apiError(w, err)
		return nil
	}
	return lib
}
