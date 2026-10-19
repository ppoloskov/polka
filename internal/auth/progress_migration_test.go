package auth

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestProgressMigration(t *testing.T) {
	for _, early := range []bool{false, true} {
		name := "before-revisions"
		if early {
			name = "before-locators"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "users.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			u, err := s.CreateUser(ctx, "reader", "password123", "Reader", RoleUser)
			if err != nil {
				t.Fatal(err)
			}
			legacy := strings.Replace(progressSchema, "\trevision   INTEGER NOT NULL DEFAULT 0,\n", "", 1)
			if early {
				legacy = strings.Replace(legacy, "\toverall    REAL NOT NULL DEFAULT 0,\n", "", 1)
				legacy = strings.Replace(legacy, "\tlocator    TEXT NOT NULL DEFAULT '',\n", "", 1)
			}
			if _, err := s.db.Exec(`DROP TABLE reading_progress;` + legacy); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`INSERT INTO reading_progress (user_id, book_id, chapter, position) VALUES (?, 42, 3, 0.25)`, u.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			p, err := s.GetProgress(ctx, u.ID, 42)
			if err != nil || p.Chapter != 3 || p.Position != 0.25 || p.Revision != 0 {
				t.Fatalf("migration changed bookmark: %+v, %v", p, err)
			}
			zero := int64(0)
			revision, err := s.SaveBridgedProgress(ctx, u.ID, 42, p, &zero, nil)
			if err != nil || revision != 1 {
				t.Fatalf("first versioned save: %d, %v", revision, err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			p, err = s.GetProgress(ctx, u.ID, 42)
			if err != nil || p.Revision != 1 || p.Chapter != 3 {
				t.Fatalf("reopen: %+v, %v", p, err)
			}
			if _, err := s.GetByLogin(ctx, "reader"); err != nil {
				t.Fatalf("user lost: %v", err)
			}
		})
	}
}
