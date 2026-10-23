package store

import (
	"context"
	"strings"
)

// PersonalBooksByIDs returns retained book cards for personal lists and
// history. Missing and disabled books keep their identity with Available=false.
func (s *Store) PersonalBooksByIDs(ctx context.Context, ids []int64, libraryIDs ...string) ([]Book, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(ids)-1) + "?"
	args := make([]any, 0, len(ids)+1)
	for _, id := range ids {
		args = append(args, id)
	}
	where := `b.deleted = 0 AND b.id IN (` + placeholders + `)`
	where, args = addLibraryScope(where, "b", scopeLibraryID(libraryIDs), args)
	books, err := s.scanBooks(ctx, `SELECT`+bookColumns+bookFrom+` WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	return orderBooksByID(ids, books), nil
}

func orderBooksByID(ids []int64, books []Book) []Book {
	byID := make(map[int64]Book, len(books))
	for _, b := range books {
		byID[b.ID] = b
	}
	ordered := make([]Book, 0, len(books))
	for _, id := range ids {
		if b, ok := byID[id]; ok {
			ordered = append(ordered, b)
		}
	}
	return ordered
}
