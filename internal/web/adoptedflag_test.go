package web

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/cycle"
	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// TestAdoptedShowIsFlaggedAdopted: the UI needs to know a show came from a
// finished-season adoption, because two things it would otherwise render are
// wrong for such a show.
//
// An adopted season has no group offsets by design — the release was chosen by
// hand, so nothing was ever trained. That made `trained` false, which in turn
// made the row render an "untrained" badge and a Train button. Both are noise
// about a question that does not apply: an adopted season is never polled, so
// there is nothing to hunt for and nothing to learn.
//
// The server-side state was already correct (showState returns Complete for an
// adopted show). What was missing was the flag the template needs to suppress
// the badge and the button.
func TestAdoptedShowIsFlaggedAdopted(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	adopted, err := st.CreateShow("Adopted Season", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSource(adopted.ID, store.SourceSeaDex); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertEpisode(adopted.ID, 1, episode.Downloaded, "", "")

	airing, err := st.CreateShow("Airing Show", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertEpisode(airing.ID, 1, episode.Downloaded, "", "")

	s := &Server{st: st}
	req := httptest.NewRequest("GET", "/shows", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var got []showJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if len(got) != 2 {
		t.Fatalf("got %d shows, want 2", len(got))
	}

	byName := map[string]showJSON{}
	for _, j := range got {
		byName[j.Name] = j
	}

	ad := byName["Adopted Season"]
	if !ad.Adopted {
		t.Error("adopted show: adopted = false, want true")
	}
	// The badge and button are gated on `trained` alone in the template, so
	// this is the combination that produced "complete · untrained".
	if ad.Trained {
		t.Error("adopted show: trained = true; an adopted season has no offsets")
	}
	// A downloaded episode in an adopted season is on disk waiting to be
	// watched, so the state is "ready to watch" rather than Complete —
	// Complete is what it becomes once nothing is outstanding.
	if ad.State != string(cycle.ReadyToWatch) {
		t.Errorf("adopted show: state = %q, want %q", ad.State, cycle.ReadyToWatch)
	}

	air := byName["Airing Show"]
	if air.Adopted {
		t.Error("airing show: adopted = true, want false")
	}
}

// TestAdoptedFlagIndependentOfWatchState: an adopted season must be flagged
// adopted while its episodes are still unwatched, so the UI can file it under
// Complete as a CATEGORY rather than waiting for every episode to be watched.
//
// The Complete section filters on `adopted`, not on state === 'complete'.
// Filtering on state would leave a freshly adopted season sitting under
// Airing, which claims kishizu is hunting it week by week when it is not.
func TestAdoptedFlagIndependentOfWatchState(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}

	sh, err := st.CreateShow("Adopted", nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSource(sh.ID, store.SourceSeaDex); err != nil {
		t.Fatal(err)
	}
	// All downloaded, none watched.
	for _, n := range []int{1, 2, 3} {
		if err := st.UpsertEpisode(sh.ID, n, episode.Downloaded, "H", "Adopted"); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/shows", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"adopted":true`) {
		t.Errorf("adopted flag not set while episodes are unwatched: %s", body)
	}
	// The state stays the actionable one, so the card can still say what to do.
	if !strings.Contains(body, `"state":"ready to watch"`) {
		t.Errorf("state should be ready to watch, got: %s", body)
	}
}
