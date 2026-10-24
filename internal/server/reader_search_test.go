package server

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Search operates on tag-stripped text: snippets clamp to the text bounds,
// and multi-byte text stays intact (no mid-rune slices).
func TestChapterPlainTextAndSnippets(t *testing.T) {
	plain, lower := chapterPlainText(`First <em>&amp;</em> paragraph<p>Вторая — привет</p>`)
	if strings.Contains(plain, "<") || strings.Contains(plain, "&amp;") {
		t.Fatalf("stripped text carries markup: %q", plain)
	}
	if plain != "First & paragraph Вторая — привет" {
		t.Fatalf("stripped text = %q", plain)
	}
	if lower != strings.ToLower(plain) {
		t.Errorf("lower form = %q", lower)
	}

	count, snippet := countMatches(lower, "привет", plain)
	if count != 1 {
		t.Fatalf("match count = %d; want 1", count)
	}
	if !utf8.ValidString(snippet) {
		t.Fatalf("snippet is not valid UTF-8: %q", snippet)
	}
	if !strings.Contains(snippet, "привет") {
		t.Errorf("snippet = %q; want the match context", snippet)
	}

	// A match at the very start: the context window clamps to the bounds.
	plain, lower = chapterPlainText("hello world")
	count, snippet = countMatches(lower, "hello", plain)
	if count != 1 {
		t.Fatalf("start match count = %d; want 1", count)
	}
	if !strings.HasPrefix(snippet, "hello") {
		t.Errorf("start snippet = %q; want the match prefix", snippet)
	}

	// A match at the very end.
	plain, lower = chapterPlainText("the end — конец")
	count, snippet = countMatches(lower, "конец", plain)
	if count != 1 {
		t.Fatalf("end match count = %d; want 1", count)
	}
	if !strings.HasSuffix(snippet, "конец") || !utf8.ValidString(snippet) {
		t.Errorf("end snippet = %q", snippet)
	}

	if count, _ := countMatches(lower, "нет", plain); count != 0 {
		t.Errorf("absent match count = %d", count)
	}
}
