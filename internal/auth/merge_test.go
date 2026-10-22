package auth

import (
	"context"
	"errors"
	"testing"
)

func TestMergeBookRefs(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	reader, err := s.CreateUser(ctx, "reader", "pass1234", "", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateUser(ctx, "other", "pass1234", "", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	const winner, loser = int64(10), int64(20)

	// Winner rows win conflicts; loser-only rows transfer.
	if err := s.SaveProgress(ctx, reader.ID, winner, Progress{Chapter: 1, Position: 0.1, Overall: 0.1}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveProgress(ctx, reader.ID, loser, Progress{Chapter: 4, Position: 0.5, Overall: 0.4}); err != nil {
		t.Fatal(err)
	}
	if err := s.RateBook(ctx, reader.ID, winner, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.RateBook(ctx, reader.ID, loser, 4); err != nil {
		t.Fatal(err)
	}
	if err := s.RateBook(ctx, other.ID, loser, 2); err != nil {
		t.Fatal(err)
	}
	both, err := s.CreateList(ctx, reader.ID, "Both")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddToList(ctx, reader.ID, both.ID, winner); err != nil {
		t.Fatal(err)
	}
	if err := s.AddToList(ctx, reader.ID, both.ID, loser); err != nil {
		t.Fatal(err)
	}
	loserOnly, err := s.CreateList(ctx, reader.ID, "Loser only")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddToList(ctx, reader.ID, loserOnly.ID, loser); err != nil {
		t.Fatal(err)
	}

	if err := s.MergeBookRefs(ctx, map[int64]int64{loser: winner}); err != nil {
		t.Fatal(err)
	}

	progress, err := s.GetProgress(ctx, reader.ID, winner)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Chapter != 1 {
		t.Fatalf("winner progress chapter = %d, want the winner's own 1", progress.Chapter)
	}
	if _, err := s.GetProgress(ctx, reader.ID, loser); !errors.Is(err, ErrNotFound) {
		t.Fatalf("loser progress after merge: %v, want not found", err)
	}
	if rating := s.UserRating(ctx, reader.ID, winner); rating != 5 {
		t.Fatalf("winner rating = %d, want the winner's own 5", rating)
	}
	if rating := s.UserRating(ctx, other.ID, winner); rating != 2 {
		t.Fatalf("transferred rating = %d, want loser's 2", rating)
	}
	if rating := s.UserRating(ctx, other.ID, loser); rating != 0 {
		t.Fatalf("loser rating after merge = %d, want removed", rating)
	}
	lists, err := s.BookListIDs(ctx, reader.ID, winner)
	if err != nil {
		t.Fatal(err)
	}
	if len(lists) != 2 {
		t.Fatalf("winner lists = %v, want both lists", lists)
	}
	lists, err = s.BookListIDs(ctx, reader.ID, loser)
	if err != nil {
		t.Fatal(err)
	}
	if len(lists) != 0 {
		t.Fatalf("loser lists after merge = %v, want none", lists)
	}

	if err := s.MergeBookRefs(ctx, nil); err != nil {
		t.Fatalf("empty merge: %v", err)
	}
}
