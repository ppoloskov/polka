package library

import (
	"bytes"
	"encoding/json"
	"html"
	"os"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

func TestFB2AnchorsAgainstCrengine(t *testing.T) {
	file, err := os.Open("testdata/kosync.fb2")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	b, err := ParseFB2Text(file, nil)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("testdata/kosync-crengine.json")
	if err != nil {
		t.Fatal(err)
	}
	var engine map[string]string
	if err = json.Unmarshal(golden, &engine); err != nil {
		t.Fatal(err)
	}
	span := regexp.MustCompile(`<span data-fb2-path="([^"]+)">(.*?)</span>`)
	count := 0
	for _, ch := range b.Chapters {
		for _, match := range span.FindAllStringSubmatch(ch.TitleHTML+ch.HTML, -1) {
			path, text := html.UnescapeString(match[1]), html.UnescapeString(match[2])
			if expected, ok := engine[path+".0"]; !ok || expected != text {
				t.Errorf("crengine mismatch %s: got %q, want %q", path, text, expected)
			}
			count++
		}
	}
	if count < 19 {
		t.Fatalf("missing source text anchors: %d", count)
	}
}

func TestFB2Windows1251Anchors(t *testing.T) {
	s := `<?xml version="1.0" encoding="windows-1251"?><FictionBook><body><section><p>Привет &amp; мир.</p></section></body></FictionBook>`
	encoded, err := charmap.Windows1251.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseFB2Text(bytes.NewReader(encoded), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.Chapters[0].HTML, "Привет &amp; мир.") || len(b.Anchors) != 1 || b.Anchors[0].Length != 13 {
		t.Fatalf("legacy encoding: %+v", b)
	}
}

func TestFB2LongRunsDoNotPublishWrongTextIndexes(t *testing.T) {
	s := `<FictionBook><body><section><p>` + strings.Repeat("word ", 2000) + `<emphasis>safe</emphasis>ambiguous</p><p>next</p></section></body></FictionBook>`
	b, err := ParseFB2Text(strings.NewReader(s), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range b.Anchors {
		if strings.Contains(a.Path, "/p[1]/text()") {
			t.Fatalf("unsafe text index: %+v", a)
		}
	}
	if len(b.Anchors) != 2 {
		t.Fatalf("safe neighbors lost: %+v", b.Anchors)
	}
}

func TestFB2SourcePositions(t *testing.T) {
	b, err := ParseFB2Text(strings.NewReader(textFB2), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		locator string
		chapter int
	}{
		{"/FictionBook/body[1]/section[1]/p/emphasis/text().3", 1},
		{"/FictionBook[1]/body[1]/section[2]/p[1]/text()[1].7", 2},
		{"/FictionBook/body/section[1]/title/p/text().2", 1},
		{"/FictionBook/body/section[1]/section/p/text().4", 1},
		{"/FictionBook/body/section[1]/poem/stanza/v[2]/text().2", 1},
		{"/FictionBook/body/section[1]/p/a/text().1", 1},
	} {
		canonical, ch, _, overall, ok := b.ResolveFB2Locator(tc.locator)
		if !ok || ch != tc.chapter || overall < 0 || overall > 1 {
			t.Errorf("resolve %s: %q %d %f %v", tc.locator, canonical, ch, overall, ok)
		}
		if again, ch2, _, _, ok := b.ResolveFB2Locator(canonical); !ok || again != canonical || ch2 != ch {
			t.Errorf("round trip %q", canonical)
		}
	}
	for _, bad := range []string{"/body/p.1", "/FictionBook/body/section[99]/p/text().0", "/FictionBook/body/section/p/text().99999", "/FictionBook/body/section/p/text().-1", "/FictionBook/body[2]/section/p/text().0"} {
		if _, _, _, _, ok := b.ResolveFB2Locator(bad); ok {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestFB2NestedChaptersKeepSourcePaths(t *testing.T) {
	s := `<FictionBook><body><section><title><p>Part</p></title><section><p>one</p></section><section><p>two</p></section><section><p>three</p></section></section></body></FictionBook>`
	b, err := ParseFB2Text(strings.NewReader(s), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, ch, _, _, ok := b.ResolveFB2Locator("/FictionBook/body/section/section[3]/p/text().2")
	if !ok || ch != 3 {
		t.Fatalf("nested source path: chapter %d, ok %v", ch, ok)
	}
}

func TestFB2UnicodeAndWhitespace(t *testing.T) {
	b, err := ParseFB2Text(strings.NewReader("<FictionBook><body><section><p>  А😀Б\n\t В&#160;Г</p></section></body></FictionBook>"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Anchors) != 1 || b.Anchors[0].Length != 8 {
		t.Fatalf("normalized Unicode anchors: %+v", b.Anchors)
	}
	if !strings.Contains(b.Chapters[0].HTML, " А😀Б В\u00a0Г") {
		t.Fatalf("text normalization: %s", b.Chapters[0].HTML)
	}
	_, _, _, _, ok := b.ResolveFB2Locator("/FictionBook/body/section/p/text().8")
	if !ok {
		t.Fatal("code point offset at end rejected")
	}
}
