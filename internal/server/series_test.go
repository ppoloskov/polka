package server

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestBookSeriesFromFB2(t *testing.T) {
	ts, client, st := newManageServer(t)
	book := strings.Replace(readerFB2, "</title-info>", `
		<sequence name="First"/>
		<sequence name="Second" number="7"><sequence name="Nested" number="2"/></sequence>
		</title-info>`, 1)
	var up map[string][]uploadResult
	decode(t, uploadFiles(t, client, ts.URL+"/admin/books/upload", map[string][]byte{"series.fb2": []byte(book)}, nil), &up)
	id := up["results"][0].BookID
	refs, err := st.BookSeries(t.Context(), id)
	if err != nil || len(refs) != 3 {
		t.Fatalf("upload lost series: %+v, %v", refs, err)
	}
	// Simulate the primary-only membership backfilled for an existing catalog.
	if _, err := st.DB().Exec(`DELETE FROM book_series WHERE book_id = ? AND series_id != ?`, id, refs[0].ID); err != nil {
		t.Fatal(err)
	}
	form := getJSONWith(t, client, ts.URL+"/main/getBooks/getBookForm?selectedItemID="+itoa64(id))
	data, err := json.Marshal(form["series"])
	if err != nil {
		t.Fatal(err)
	}
	var series []struct {
		SeriesID    int64
		SeriesTitle string
		SeqNumber   int
	}
	if err := json.Unmarshal(data, &series); err != nil {
		t.Fatal(err)
	}
	if len(series) != 3 || series[0].SeriesTitle != "First" || series[0].SeqNumber != 0 || series[1].SeqNumber != 7 || series[2].SeqNumber != 2 {
		t.Fatalf("book view lost series/numbers: %+v", series)
	}
	for _, ref := range series {
		page := getJSONWith(t, client, ts.URL+"/main/getBooks/getSearchSeriesBooks?selectedItemID="+strconv.FormatInt(ref.SeriesID, 10))
		books := page["titlesList"].([]any)
		if page["title"] != ref.SeriesTitle || len(books) != 1 || books[0].(map[string]any)["SeqNumber"] != float64(ref.SeqNumber) {
			t.Fatalf("series link %s returned wrong membership: %v", ref.SeriesTitle, page)
		}
	}
}
