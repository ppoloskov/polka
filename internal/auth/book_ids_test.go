package auth

import (
	"context"
	"testing"
)

func TestMaxBookID(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	if got, err := s.MaxBookID(ctx); err != nil || got != 0 {
		t.Fatalf("empty users DB: MaxBookID() = %d, %v; want 0, nil", got, err)
	}
	u, err := s.CreateUser(ctx, "reader", "secret123", "", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveProgress(ctx, u.ID, 100, Progress{Chapter: 1}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.MaxBookID(ctx); err != nil || got != 100 {
		t.Fatalf("progress reference: MaxBookID() = %d, %v; want 100, nil", got, err)
	}
	if err := s.RateBook(ctx, u.ID, 200, 4); err != nil {
		t.Fatal(err)
	}
	if got, err := s.MaxBookID(ctx); err != nil || got != 200 {
		t.Fatalf("rating reference: MaxBookID() = %d, %v; want 200, nil", got, err)
	}
	list, err := s.CreateList(ctx, u.ID, "Later")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddToList(ctx, u.ID, list.ID, 300); err != nil {
		t.Fatal(err)
	}
	if got, err := s.MaxBookID(ctx); err != nil || got != 300 {
		t.Fatalf("list reference: MaxBookID() = %d, %v; want 300, nil", got, err)
	}
}
