package auth

import "context"

// BookRefPair is one loser→winner book-ID transfer. Ordered slices matter:
// when a winner is itself the loser of a later pair (published later), an
// earlier pair must move its rows first, so replay in publication order
// carries chained state onto the final winner.
type BookRefPair struct {
	Loser  int64
	Winner int64
}

// MergeBookRefs transfers per-book personal state from retired duplicate book
// IDs to the kept IDs, applying the given pairs in slice order. Merges are
// exact by construction (INPX file-group consolidation); never fuzzy. The
// winner's existing rows win conflicts, loser-only rows move.
func (s *Service) MergeBookRefs(ctx context.Context, merges []BookRefPair) error {
	if len(merges) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, pair := range merges {
		loser, winner := pair.Loser, pair.Winner
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
