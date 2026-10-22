package importer

import (
	"io"
	"os"
	"strings"

	"github.com/vestigiumincaligne/polka/internal/library"
	"github.com/vestigiumincaligne/polka/internal/store"
)

// BookFromFile extracts the metadata supported by the upload path. On parse
// failure it returns a filename-based fallback together with the error.
func BookFromFile(path, base, ext string) (*store.BookInput, error) {
	f, err := os.Open(path)
	if err != nil {
		return &store.BookInput{Title: base}, err
	}
	defer f.Close()
	return BookFromOpenedFile(f, base, ext)
}

// BookFromOpenedFile extracts metadata from an already-open source file.
func BookFromOpenedFile(f *os.File, base, ext string) (*store.BookInput, error) {
	input := &store.BookInput{Title: base}
	switch strings.ToLower(ext) {
	case "fb2":
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return input, err
		}
		meta, err := library.ParseFB2(f, false)
		if err != nil {
			return input, err
		}
		if meta.Title != "" {
			input.Title = meta.Title
		}
		for _, a := range meta.Authors {
			input.Authors = append(input.Authors, store.AuthorName{Last: a.Last, First: a.First, Middle: a.Middle})
		}
		input.Series, input.SeriesNum = meta.Series, meta.SeriesNum
		for _, sequence := range meta.Sequences {
			input.SeriesList = append(input.SeriesList, store.SeriesRef{Title: sequence.Name, SeqNumber: sequence.Number})
		}
		input.Genres, input.Lang, input.ISBN = meta.Genres, meta.Lang, meta.ISBN
	case "epub":
		info, err := f.Stat()
		if err != nil {
			return input, err
		}
		meta, err := library.ParseEPUB(f, info.Size())
		if err != nil {
			return input, err
		}
		if meta.Title != "" {
			input.Title = meta.Title
		}
		for _, name := range meta.Authors {
			input.Authors = append(input.Authors, store.AuthorName{Last: name})
		}
		input.Lang = meta.Language
		input.Keywords = meta.Subjects
		input.Genres = library.GenresFromSubjects(meta.Subjects)
	}
	return input, nil
}
