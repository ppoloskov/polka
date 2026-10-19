package library

import (
	"bufio"
	"encoding/xml"
	"fmt"
	stdhtml "html"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// Keep only the decoder's unread bytes so entity-originated whitespace can be
// distinguished from literal XML whitespace. crengine collapses the latter
// before decoding entities. Decode legacy FB2 encodings before tracking offsets.
type fb2Input struct {
	reader io.Reader
	buffer []byte
	base   int64
}

var fb2Encoding = regexp.MustCompile(`(?i)encoding\s*=\s*["']([^"']+)["']`)

func newFB2Input(r io.Reader) (*fb2Input, error) {
	buffered := bufio.NewReader(r)
	header, _ := buffered.Peek(512)
	var decoded io.Reader = buffered
	if i := strings.Index(string(header), "?>"); i >= 0 {
		if match := fb2Encoding.FindSubmatch(header[:i]); len(match) > 0 {
			var err error
			decoded, err = charset.NewReaderLabel(string(match[1]), buffered)
			if err != nil {
				return nil, err
			}
		}
	}
	return &fb2Input{reader: decoded}, nil
}

func (r *fb2Input) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.buffer = append(r.buffer, p[:n]...)
	return n, err
}

func (r *fb2Input) consume(offset int64) string {
	n := int(offset - r.base)
	if n < 0 || n > len(r.buffer) {
		return ""
	}
	s := string(r.buffer[:n])
	r.buffer = r.buffer[n:]
	r.base = offset
	return s
}

// FB2Anchor connects rendered text to a normalized crengine xpointer. Offsets
// are Unicode code points (crengine uses lChar32), not bytes or UTF-16 units.
type FB2Anchor struct {
	Path    string
	Length  int
	Chapter int
	Start   int
}

type fb2Frame struct {
	path       string
	counts     map[string]int
	children   int
	block      bool
	unsafeText bool
}

// fb2Tokens observes *every* token, including subtrees consumed by Skip and
// collectTitle. Same-name sibling indexes must not depend on HTML conversion.
type fb2Tokens struct {
	dec        *xml.Decoder
	input      *fb2Input
	stack      []fb2Frame
	path       string
	lastOffset int64
}

func (s *fb2Tokens) Token() (xml.Token, error) {
	t, err := s.dec.Token()
	if err != nil {
		return nil, err
	}
	consumed := s.dec.InputOffset() - s.lastOffset
	s.lastOffset = s.dec.InputOffset()
	raw := s.input.consume(s.lastOffset)
	switch v := t.(type) {
	case xml.StartElement:
		path := ""
		if len(s.stack) > 0 {
			p := &s.stack[len(s.stack)-1]
			p.counts[v.Name.Local]++
			p.children++
			path = p.path + "/" + v.Name.Local + "[" + strconv.Itoa(p.counts[v.Name.Local]) + "]"
		} else {
			path = "/" + v.Name.Local + "[1]"
		}
		// FB2's inline elements allow an initial whitespace text node; block
		// elements discard it, as ldomElementWriter::onText does in crengine.
		inline := strings.Contains("|emphasis|strong|strikethrough|sub|sup|code|a|style|", "|"+v.Name.Local+"|")
		s.stack = append(s.stack, fb2Frame{path: path, counts: map[string]int{}, block: !inline})

	case xml.EndElement:
		if len(s.stack) > 0 {
			s.stack = s.stack[:len(s.stack)-1]
		}
	case xml.CharData:
		if len(s.stack) == 0 {
			return t, nil
		}
		p := &s.stack[len(s.stack)-1]
		text := normalizeFB2RawText(raw)
		if p.block && p.children == 0 && strings.Trim(text, " ") == "" {
			return xml.CharData(nil), nil
		}
		p.counts["text()"]++
		p.children++
		s.path = p.path + "/text()[" + strconv.Itoa(p.counts["text()"]) + "]"
		// crengine splits raw XML text at 8192 characters before entity
		// decoding. Avoid publishing wrong sibling indexes for such runs;
		// their subsequent direct text siblings are ambiguous too.
		if consumed >= 8192 && utf8.RuneCountInString(raw) >= 8192 {
			p.unsafeText = true
		}
		if p.unsafeText {
			s.path = ""
		}
		t = xml.CharData(text)
	}
	return t, nil
}

func normalizeFB2RawText(raw string) string {
	cdata := strings.HasPrefix(raw, "<![CDATA[")
	if cdata {
		raw = strings.TrimSuffix(strings.TrimPrefix(raw, "<![CDATA["), "]]>")
	}
	var b strings.Builder
	space := false
	for len(raw) > 0 {
		if raw[0] == '&' && !cdata {
			if end := strings.IndexByte(raw, ';'); end >= 0 {
				entity := raw[:end+1]
				decoded := stdhtml.UnescapeString(entity)
				if decoded != entity {
					b.WriteString(decoded)
					space = false
					raw = raw[end+1:]
					continue
				}
			}
		}
		r, size := utf8.DecodeRuneInString(raw)
		raw = raw[size:]
		if r == '\r' || r == '\n' || r == '\t' {
			r = ' '
		}
		if r != ' ' || !space {
			b.WriteRune(r)
		}
		space = r == ' '
	}
	return b.String()
}

var fb2Step = regexp.MustCompile(`^(FictionBook|[a-zA-Z][a-zA-Z0-9_-]*|text\(\))(?:\[([1-9][0-9]*)\])?$`)

// CanonicalFB2Locator accepts both old implicit-first indexes and crengine's
// newer explicit [1] form. Reject foreign formats, boxing paths and bad offsets.
func CanonicalFB2Locator(locator string) (string, int, bool) {
	if len(locator) > 2048 {
		return "", 0, false
	}
	path, offset := locator, 0
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		n, err := strconv.Atoi(path[i+1:])
		if err != nil || n < 0 {
			return "", 0, false
		}
		offset = n
		path = path[:i]
	}
	steps := strings.Split(path, "/")
	if len(steps) < 3 || steps[0] != "" {
		return "", 0, false
	}
	for i := 1; i < len(steps); i++ {
		m := fb2Step.FindStringSubmatch(steps[i])
		if m == nil {
			return "", 0, false
		}
		index := m[2]
		if index == "" {
			index = "1"
		}
		steps[i] = m[1] + "[" + index + "]"
	}
	if steps[1] != "FictionBook[1]" || !strings.HasPrefix(steps[2], "body[") {
		return "", 0, false
	}
	return strings.Join(steps, "/"), offset, true
}

func (b *FB2Text) indexFB2Anchors() {
	for chapter, ch := range b.Chapters {
		doc, err := html.Parse(strings.NewReader(ch.TitleHTML + ch.HTML))
		if err != nil {
			continue
		}
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			for _, a := range n.Attr {
				if a.Key == "data-fb2-path" {
					length := 0
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == html.TextNode {
							length += utf8.RuneCountInString(c.Data)
						}
					}
					if length > 0 {
						b.Anchors = append(b.Anchors, FB2Anchor{a.Val, length, chapter, b.TextLength})
						b.TextLength += length
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
	}
}

// ResolveFB2Locator returns a canonical text location and a legacy chapter
// fraction fallback. Element positions (titles, sections) use their first
// rendered descendant. Missing/unrendered nodes are never guessed by index.
func (b *FB2Text) ResolveFB2Locator(locator string) (string, int, float64, float64, bool) {
	path, offset, ok := CanonicalFB2Locator(locator)
	if !ok {
		return "", 0, 0, 0, false
	}
	for _, a := range b.Anchors {
		if a.Path != path && !strings.HasPrefix(a.Path, path+"/") {
			continue
		}
		if a.Path != path && offset != 0 {
			return "", 0, 0, 0, false
		}
		if offset > a.Length {
			return "", 0, 0, 0, false
		}
		chapterStart, chapterLength := a.Start, 0
		for _, other := range b.Anchors {
			if other.Chapter == a.Chapter {
				if other.Start < chapterStart {
					chapterStart = other.Start
				}
				chapterLength += other.Length
			}
		}
		position := float64(a.Start-chapterStart+offset) / float64(max(1, chapterLength))
		overall := float64(a.Start+offset) / float64(max(1, b.TextLength))
		return fmt.Sprintf("%s.%d", a.Path, offset), a.Chapter, position, overall, true
	}
	return "", 0, 0, 0, false
}

var _ xml.TokenReader = (*fb2Tokens)(nil)
