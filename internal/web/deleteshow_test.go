package web

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// TestDeleteShowRemovesEverything: removing a show must take its aliases,
// offsets and episodes with it, and clear the dedupe table.
//
// `seen` has no foreign key, so it needs an explicit delete — leaving rows
// behind would keep dedupe entries for a show that no longer exists, and a
// re-added season would silently skip releases already seen.
func TestDeleteShowRemovesEverything(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	sh, _ := st.CreateShow("Show", []string{"Alt Name"}, 12)
	_ = st.SetGroupOffset(sh.ID, "VARYG", 40, "test")
	_ = st.UpsertEpisode(sh.ID, 1, episode.Watched, "", "")
	_ = st.MarkSeen("deadbeef", sh.ID, 1)

	s := &Server{st: st}
	body := `{"id": ` + fmt.Sprintf("%d", sh.ID) + `}`
	req := httptest.NewRequest("DELETE", "/api/shows", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("delete: status %d: %s", rec.Code, rec.Body.String())
	}

	if got, _ := st.GetShow(sh.ID); got != nil {
		t.Error("show still present after delete")
	}
	// A re-created show must come back clean: no aliases, episodes or
	// offsets inherited from the deleted one.
	again, err := st.CreateShow("Show", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	if eps, _ := st.EpisodesForShow(again.ID); len(eps) != 0 {
		t.Errorf("%d episodes inherited by the re-created show", len(eps))
	}
	if offs, _ := st.GroupOffsets(again.ID); len(offs) != 0 {
		t.Errorf("%d offsets inherited by the re-created show", len(offs))
	}
	if seen, _ := st.HasSeen("deadbeef"); seen {
		t.Error("dedupe entry survived the delete")
	}
}

// TestDeleteShowUnknownID: a missing show is a 404, not a silent success.
func TestDeleteShowUnknownID(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "test.db"))
	defer st.Close()

	s := &Server{st: st}
	req := httptest.NewRequest("DELETE", "/api/shows", strings.NewReader(`{"id": 9999}`))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
