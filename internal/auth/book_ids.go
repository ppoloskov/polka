package auth

import "context"

// MaxBookID returns the largest catalog ID referenced by any persisted user state.
func (s *Service) MaxBookID(ctx context.Context) (int64, error) {
	var maxID int64
	err := s.db.QueryRowContext(ctx, `
		SELECT max(
			coalesce((SELECT max(book_id) FROM reading_progress), 0),
			coalesce((SELECT max(book_id) FROM book_ratings), 0),
			coalesce((SELECT max(book_id) FROM list_books), 0)
		)`).Scan(&maxID)
	return maxID, err
}
