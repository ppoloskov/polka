package library

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html/charset"
)

// FB2Meta holds book metadata from an FB2 file.
type FB2Meta struct {
	Title          string
	Authors        []PersonName
	Series         string
	SeriesNum      int
	Sequences      []Sequence
	Genres         []string
	Lang           string
	AnnotationHTML string
	Publisher      string
	City           string
	Year           string
	ISBN           string
	Cover          []byte
	CoverMime      string
}

// Sequence is one FB2 series membership, including its own book number.
type Sequence struct {
	Name   string
	Number int
}

type PersonName struct {
	First, Middle, Last string
}

// ParseFB2 parses an FB2 as a stream. With withCover=true it reads on
// until the <binary> with the cover; otherwise it stops after <description>.
func ParseFB2(r io.Reader, withCover bool) (*FB2Meta, error) {
	dec := xml.NewDecoder(r)
	dec.CharsetReader = charset.NewReaderLabel
	dec.Strict = false

	meta := &FB2Meta{}
	var coverID string
	var path []string

	in := func(elems ...string) bool {
		if len(path) < len(elems) {
			return false
		}
		tail := path[len(path)-len(elems):]
		for i := range elems {
			if tail[i] != elems[i] {
				return false
			}
		}
		return true
	}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("fb2 parse: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			switch {
			case name == "annotation" && in("description", "title-info"):
				htmlStr, err := annotationToHTML(dec, t)
				if err != nil {
					return nil, err
				}
				meta.AnnotationHTML = htmlStr
				continue // annotationToHTML has already consumed the EndElement

			case name == "image" && in("description", "title-info", "coverpage"):
				for _, a := range t.Attr {
					if a.Name.Local == "href" {
						coverID = strings.TrimPrefix(a.Value, "#")
					}
				}

			case name == "author" && in("description", "title-info"):
				meta.Authors = append(meta.Authors, PersonName{})

			case name == "sequence" && len(path) >= 3 && path[1] == "description" && path[2] == "title-info":
				sequence := Sequence{}
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "name":
						sequence.Name = strings.TrimSpace(a.Value)
					case "number":
						sequence.Number, _ = strconv.Atoi(a.Value)
					}
				}
				if sequence.Name != "" {
					if len(meta.Sequences) == 0 {
						meta.Series, meta.SeriesNum = sequence.Name, sequence.Number
					}
					meta.Sequences = append(meta.Sequences, sequence)
				}

			case name == "binary":
				var id, ctype string
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "id":
						id = a.Value
					case "content-type":
						ctype = a.Value
					}
				}
				// Without a declared coverpage, treat the book's first image
				// as the cover — a common case in real-world collections.
				take := coverID != "" && id == coverID ||
					coverID == "" && strings.HasPrefix(ctype, "image/")
				if withCover && take {
					var b64 string
					if err := dec.DecodeElement(&b64, &t); err != nil {
						return nil, err
					}
					data, err := base64.StdEncoding.DecodeString(strings.Map(dropSpace, b64))
					if err != nil {
						return nil, fmt.Errorf("cover base64: %w", err)
					}
					meta.Cover = data
					meta.CoverMime = ctype
					return meta, nil
				}
				if err := dec.Skip(); err != nil {
					return nil, err
				}
				continue
			}
			path = append(path, name)

		case xml.EndElement:
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
			if t.Name.Local == "description" && !withCover {
				return meta, nil
			}

		case xml.CharData:
			if len(path) < 2 {
				continue
			}
			text := strings.TrimSpace(string(t))
			if text == "" {
				continue
			}
			elem, parent := path[len(path)-1], path[len(path)-2]
			switch parent {
			case "publish-info":
				switch elem {
				case "publisher":
					meta.Publisher += text
				case "city":
					meta.City += text
				case "year":
					meta.Year += text
				case "isbn":
					meta.ISBN += text
				}
			case "title-info":
				switch elem {
				case "book-title":
					meta.Title += text
				case "lang":
					meta.Lang += strings.ToLower(text)
				case "genre":
					meta.Genres = append(meta.Genres, text)
				}
			case "author":
				if len(path) < 3 || path[len(path)-3] != "title-info" || len(meta.Authors) == 0 {
					continue
				}
				a := &meta.Authors[len(meta.Authors)-1]
				switch elem {
				case "first-name":
					a.First += text
				case "middle-name":
					a.Middle += text
				case "last-name":
					a.Last += text
				}
			}
		}
	}
	return meta, nil
}

func decodeBase64(b64 string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(strings.Map(dropSpace, b64))
}

func dropSpace(r rune) rune {
	if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
		return -1
	}
	return r
}

// annotationTags maps annotation fb2 elements to their HTML counterparts.
var annotationTags = map[string]string{
	"p":             "p",
	"emphasis":      "em",
	"strong":        "strong",
	"strikethrough": "s",
	"sub":           "sub",
	"sup":           "sup",
	"code":          "code",
	"cite":          "blockquote",
	"poem":          "blockquote",
	"stanza":        "p",
	"subtitle":      "h4",
	"table":         "table",
	"tr":            "tr",
	"td":            "td",
	"th":            "th",
}

// annotationToHTML reads the <annotation> subtree and converts it to HTML.
func annotationToHTML(dec *xml.Decoder, _ xml.StartElement) (string, error) {
	var sb strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return "", fmt.Errorf("annotation: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "empty-line":
				sb.WriteString("<br/>")
			case "v": // verse line
			default:
				if tag, ok := annotationTags[t.Name.Local]; ok {
					sb.WriteString("<" + tag + ">")
				}
			}
		case xml.EndElement:
			depth--
			if depth == 0 {
				break
			}
			switch t.Name.Local {
			case "empty-line":
			case "v":
				sb.WriteString("<br/>")
			default:
				if tag, ok := annotationTags[t.Name.Local]; ok {
					sb.WriteString("</" + tag + ">")
				}
			}
		case xml.CharData:
			sb.WriteString(html.EscapeString(string(t)))
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

var binaryRe = regexp.MustCompile(`(?s)<binary[^>]*>.*?</binary>`)

// StripBinaries removes all embedded images (<binary>) from an FB2.
func StripBinaries(data []byte) []byte {
	return binaryRe.ReplaceAll(data, nil)
}

// fb2MissingBinaries returns FB2 image ids referenced by <image href="#…">
// elements but not found among the <binary id> payloads, in reference order.
// Unparseable files return nil.
func fb2MissingBinaries(data []byte) []string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = charset.NewReaderLabel
	dec.Strict = false
	var referenced, embedded []string
	hrefRe := regexp.MustCompile(`^#(.+)`)
	for {
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			// Decode errors: fall back to the exact original bytes.
			return nil
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "image":
			for _, a := range start.Attr {
				if a.Name.Local == "href" {
					if m := hrefRe.FindStringSubmatch(a.Value); m != nil {
						referenced = append(referenced, m[1])
					}
				}
			}
		case "binary":
			id := attrValue(start, "id")
			if id != "" {
				embedded = append(embedded, id)
				// skip the base64 payload quickly
				var skip string
				if err := dec.DecodeElement(&skip, &start); err != nil {
					return nil
				}
			}
		}
	}
	seen := make(map[string]bool, len(embedded))
	for _, id := range embedded {
		seen[id] = true
	}
	var missing []string
	for _, id := range referenced {
		if !seen[id] {
			seen[id] = true // also dedupe repeated references
			missing = append(missing, id)
		}
	}
	return missing
}

// ReembedBinaries inserts the given payloads (fb2 image id -> raw image
// bytes) as <binary> elements before the closing </FictionBook>. Ids come
// from the source document and are preserved whole: the elements are emitted
// with encoding/xml (ids are escaped, never truncated), while unprefixed
// element and attribute names keep them in the document's default namespace
// the way existing binaries are written. The image type is sniffed from the
// bytes (sidecar entries carry no extensions). Returns ErrNoFile when no
// insertion point is found.
func ReembedBinaries(data []byte, ids []string, payloads map[string][]byte) ([]byte, error) {
	cut := bytes.LastIndex(data, []byte("</FictionBook>"))
	if cut < 0 {
		return nil, ErrNoFile
	}
	var buf bytes.Buffer
	buf.Grow(len(data) + 256)
	buf.Write(data[:cut])
	enc := xml.NewEncoder(&buf)
	for _, id := range ids {
		payload, ok := payloads[id]
		if !ok || len(payload) == 0 {
			continue
		}
		start := xml.StartElement{
			Name: xml.Name{Local: "binary"},
			Attr: []xml.Attr{
				{Name: xml.Name{Local: "id"}, Value: id},
				{Name: xml.Name{Local: "content-type"}, Value: sniffImageMime(payload)},
			},
		}
		if err := enc.EncodeToken(start); err != nil {
			return nil, err
		}
		b64 := base64.StdEncoding.EncodeToString(payload)
		var wrapped strings.Builder
		for len(b64) > 76 {
			wrapped.WriteString(b64[:76])
			wrapped.WriteByte('\n')
			b64 = b64[76:]
		}
		wrapped.WriteString(b64)
		if err := enc.EncodeToken(xml.CharData(wrapped.String())); err != nil {
			return nil, err
		}
		if err := enc.EncodeToken(start.End()); err != nil {
			return nil, err
		}
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	buf.Write(data[cut:])
	return buf.Bytes(), nil
}
