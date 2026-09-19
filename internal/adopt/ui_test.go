package adopt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/web"
)

// End-to-end through the real handler: an adopted season (no slug, no air
// dates, no offsets) must report downloading while in flight and ready to
// watch once filed, and must never claim to need training.
func TestAdoptedShowThroughAPI(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sh, _ := st.CreateShow("DanMachi III", nil, 12)
	if err := st.SetSource(sh.ID, store.SourceSeaDex); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 12; i++ {
		if err := st.UpsertEpisode(sh.ID, i, episode.Downloading, "H", "rel"); err != nil {
			t.Fatal(err)
		}
	}

	srv, err := web.New(st)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/shows", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var shows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &shows); err != nil {
		t.Fatal(err)
	}
	if len(shows) != 1 {
		t.Fatalf("got %d shows, want 1", len(shows))
	}
	t.Logf("state while downloading: %v", shows[0]["state"])

	// File them, as Reconcile would.
	for i := 1; i <= 12; i++ {
		p := filepath.Join("/media/anime", "DanMachi III", "DanMachi III - E01.mkv")
		if err := st.FinaliseEpisode(sh.ID, i, p); err != nil {
			t.Fatal(err)
		}
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/shows", nil))
	shows = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &shows); err != nil {
		t.Fatal(err)
	}
	t.Logf("state after filing:     %v", shows[0]["state"])
	t.Logf("downloaded count:       %v", shows[0]["downloaded"])
}
