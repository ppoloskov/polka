package auth

import (
	"context"
	"errors"
	"fmt"
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

	if err := s.MergeBookRefs(ctx, []BookRefPair{{Loser: loser, Winner: winner}}); err != nil {
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

// Chained consolidations replay in publication order: an earlier pair moves
// rows onto its winner before a later pair migrates that winner's rows
// (including just-moved ones) onto the final winner. Reversing the order
// strands state on an intermediate winner — exactly the bug a map-based
// merge cannot avoid. The repetitions make a wrong-order implementation
// fail reliably instead of flakily.
func TestMergeBookRefsChainedReplayFollowsOrder(t *testing.T) {
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

	for round := int64(0); round < 20; round++ {
		a, b := int64(30+round*10), int64(20+round*10)
		if err := s.SaveProgress(ctx, reader.ID, a, Progress{Chapter: 3, Position: 0.7, Overall: 0.7}); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveProgress(ctx, reader.ID, b, Progress{Chapter: 8, Position: 0.9, Overall: 0.9}); err != nil {
			t.Fatal(err)
		}
		if err := s.RateBook(ctx, other.ID, a, 4); err != nil {
			t.Fatal(err)
		}
		list, err := s.CreateList(ctx, reader.ID, fmt.Sprintf("round-%d", round))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.AddToList(ctx, reader.ID, list.ID, a); err != nil {
			t.Fatal(err)
		}
		// Publication order: a publish retired a with winner b, then a
		// later publish retired that b with winner final(10).
		final := int64(10 + round*10)
		if err := s.MergeBookRefs(ctx, []BookRefPair{{Loser: a, Winner: b}, {Loser: b, Winner: final}}); err != nil {
			t.Fatal(err)
		}
		progress, err := s.GetProgress(ctx, reader.ID, final)
		if err != nil {
			t.Fatalf("round %d: final progress: %v", round, err)
		}
		// The b row moves after the a row, so "winner wins conflicts"
		// leaves b's (8th chapter) position as the survivor.
		if progress.Chapter != 8 {
			t.Fatalf("round %d: final progress chapter = %d, want 8 (rows moved from both losers)", round, progress.Chapter)
		}
		for _, id := range []int64{a, b} {
			if _, err := s.GetProgress(ctx, reader.ID, id); !errors.Is(err, ErrNotFound) {
				t.Fatalf("round %d: state stranded on %d: %v", round, id, err)
			}
		}
		if rating := s.UserRating(ctx, other.ID, final); rating != 4 {
			t.Fatalf("round %d: rating = %d, want transferred 4", round, rating)
		}
		listIDs, err := s.BookListIDs(ctx, reader.ID, final)
		if err != nil {
			t.Fatal(err)
		}
		if len(listIDs) != 1 {
			t.Fatalf("round %d: list membership = %v, want moved onto the final winner", round, listIDs)
		}
	}
}

// Recovered merges replay recorded pairs after a crash; replaying against
// an already-merged state must be an exact no-op for every user.
func TestMergeBookRefsReplayIsNoop(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	reader, err := s.CreateUser(ctx, "reader", "pass1234", "", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	const winner, loser = int64(30), int64(40)
	if err := s.SaveProgress(ctx, reader.ID, loser, Progress{Chapter: 2, Position: 0.4, Overall: 0.4}); err != nil {
		t.Fatal(err)
	}
	if err := s.RateBook(ctx, reader.ID, loser, 3); err != nil {
		t.Fatal(err)
	}
	list, err := s.CreateList(ctx, reader.ID, "Read")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddToList(ctx, reader.ID, list.ID, loser); err != nil {
		t.Fatal(err)
	}
	merge := []BookRefPair{{Loser: loser, Winner: winner}}
	if err := s.MergeBookRefs(ctx, merge); err != nil {
		t.Fatal(err)
	}
	counts := func() int {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM reading_progress WHERE user_id=?`, reader.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		var r, l int
		s.db.QueryRow(`SELECT count(*) FROM book_ratings WHERE user_id=?`, reader.ID).Scan(&r)
		s.db.QueryRow(`SELECT count(*) FROM list_books WHERE list_id=?`, list.ID).Scan(&l)
		return n + r + l
	}
	before := counts()
	// The pending-pair table is acknowledged only after this merge, so a
	// crash between merge and acknowledge replays exactly this call.
	if err := s.MergeBookRefs(ctx, merge); err != nil {
		t.Fatal(err)
	}
	if after := counts(); after != before {
		t.Fatalf("replay changed state: %d rows after, want %d", after, before)
	}
	if _, err := s.GetProgress(ctx, reader.ID, loser); !errors.Is(err, ErrNotFound) {
		t.Errorf("loser progress came back on replay: %v", err)
	}
}

// Consolidation transfers bookmarks together with the rest of the personal
// state, and the reference floor counts them.
func TestMergeBookRefsTransfersBookmarks(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	reader, err := s.CreateUser(ctx, "reader", "pass1234", "", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	const loser, winner = int64(70), int64(80)
	if _, err := s.AddBookmark(ctx, reader.ID, loser, 3, 0.4, "/FictionBook[1]/body[1]/p[2]/text()[1].12", "Глава 3"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddBookmark(ctx, reader.ID, winner, 1, 0.1, "", ""); err != nil {
		t.Fatal(err)
	}
	maxID, err := s.MaxBookID(ctx)
	if err != nil || maxID < winner {
		t.Fatalf("MaxBookID = %d, %v; want at least the winner id %d", maxID, err, winner)
	}
	if err := s.MergeBookRefs(ctx, []BookRefPair{{Loser: loser, Winner: winner}}); err != nil {
		t.Fatal(err)
	}
	winnerBookmarks, err := s.Bookmarks(ctx, reader.ID, winner)
	if err != nil || len(winnerBookmarks) != 2 {
		t.Fatalf("winner bookmarks = %+v, %v; want both", winnerBookmarks, err)
	}
	loserBookmarks, err := s.Bookmarks(ctx, reader.ID, loser)
	if err != nil || len(loserBookmarks) != 0 {
		t.Fatalf("loser bookmarks = %+v, %v; want none left", loserBookmarks, err)
	}
}
