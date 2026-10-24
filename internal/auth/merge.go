package auth

import "context"

// MergeBookRefs transfers per-book personal state from retired duplicate book
// IDs to the kept ID. merges maps loser -> winner and is exact by
// construction (INPX file-group consolidation); never fuzzy. The winner's
// existing rows win conflicts, loser-only rows move.
func (s *Service) MergeBookRefs(ctx context.Context, merges map[int64]int64) error {
	if len(merges) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for loser, winner := range merges {
		if loser == winner {
			continue
		}
		for _, table := range []struct {
			insert string
			remove string
		}{
			{
				`INSERT OR IGNORE INTO reading_progress (user_id, book_id, chapter, position, overall, locator, revision, updated_at)
				 SELECT user_id, ?, chapter, position, overall, locator, revision, updated_at FROM reading_progress WHERE book_id = ?`,
				`DELETE FROM reading_progress WHERE book_id = ?`,
			},
			{
				`INSERT OR IGNORE INTO book_ratings (user_id, book_id, rating, updated_at)
				 SELECT user_id, ?, rating, updated_at FROM book_ratings WHERE book_id = ?`,
				`DELETE FROM book_ratings WHERE book_id = ?`,
			},
			{
				`INSERT OR IGNORE INTO list_books (list_id, book_id, added_at)
				 SELECT list_id, ?, added_at FROM list_books WHERE book_id = ?`,
				`DELETE FROM list_books WHERE book_id = ?`,
			},
		} {
			if _, err := tx.ExecContext(ctx, table.insert, winner, loser); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, table.remove, loser); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
