package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// webhookServer builds a server with one tracked show for webhook tests.
func webhookServer(t *testing.T) (*Server, *store.Show) {
	t.Helper()
	srv := testServer(t)
	sh, err := srv.st.CreateShow("Tomb Raider King", []string{"Tomb Raider King"}, 12)
	if err != nil {
		t.Fatalf("create show: %v", err)
	}
	if err := srv.st.SetGroupOffset(sh.ID, "ToonsHub", 0, "training"); err != nil {
		t.Fatalf("offset: %v", err)
	}
	return srv, sh
}

// TestWebhookJellyfinShape: Jellyfin posts {"Event":"item.markplayed","Item":{...}}
// with the series name and episode index. The episode must be latched watched.
func TestWebhookJellyfinShape(t *testing.T) {
	srv, sh := webhookServer(t)
	body := `{"Event":"item.markplayed","Item":{"SeriesName":"Tomb Raider King","IndexNumber":9,"Path":"/media/Show - E09.mkv"}}`
	rec := post(t, srv, "/api/webhook", body)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"marked":true`) {
		t.Errorf("body = %s, want marked:true", rec.Body.String())
	}
	ep, _ := srv.st.GetEpisode(sh.ID, 9)
	if ep == nil || ep.State != episode.Watched {
		t.Errorf("episode not marked watched: %+v", ep)
	}
}

// TestWebhookPlexShape: Plex posts multipart/form-data with a "payload" field
// holding JSON that names the show as grandparentTitle and the episode as
// index. Plex sends no file path, so the name path must carry it.
func TestWebhookPlexShape(t *testing.T) {
	srv, sh := webhookServer(t)
	req := httptest.NewRequest("POST", "/api/webhook", strings.NewReader(
		"--X\r\nContent-Disposition: form-data; name=\"payload\"\r\n\r\n"+
			`{"event":"media.scrobble","Metadata":{"grandparentTitle":"Tomb Raider King","index":9}}`+
			"\r\n--X--\r\n"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=X")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	ep, _ := srv.st.GetEpisode(sh.ID, 9)
	if ep == nil || ep.State != episode.Watched {
		t.Errorf("episode not marked watched: %+v", ep)
	}
}

// TestWebhookGenericShape: a hand-written POST with show + episode works, so
// the endpoint is usable without any media server.
func TestWebhookGenericShape(t *testing.T) {
	srv, sh := webhookServer(t)
	rec := post(t, srv, "/api/webhook", `{"show":"Tomb Raider King","episode":9}`)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	ep, _ := srv.st.GetEpisode(sh.ID, 9)
	if ep == nil || ep.State != episode.Watched {
		t.Errorf("episode not marked watched: %+v", ep)
	}
}

// TestWebhookIgnoresNonWatchEvents: media servers fire playback.start and
// friends constantly. Those must be answered OK without marking anything,
// or the log fills with failures for the server behaving normally.
func TestWebhookIgnoresNonWatchEvents(t *testing.T) {
	srv, sh := webhookServer(t)
	body := `{"Event":"playback.start","Item":{"SeriesName":"Tomb Raider King","IndexNumber":9}}`
	rec := post(t, srv, "/api/webhook", body)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	ep, _ := srv.st.GetEpisode(sh.ID, 9)
	if ep != nil {
		t.Errorf("non-watch event marked an episode: %+v", ep)
	}
}

// TestWebhookRefusesUnknownShow: a name that matches no tracked show or alias
// must refuse, not guess. A wrong guess here latches the wrong episode.
func TestWebhookRefusesUnknownShow(t *testing.T) {
	srv, _ := webhookServer(t)
	rec := post(t, srv, "/api/webhook", `{"show":"Not A Tracked Show","episode":9}`)
	if rec.Code != 422 {
		t.Errorf("status %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

// TestWebhookMatchesByAlias: media servers send the name as their library
// spells it, which is usually an alias rather than the canonical name.
func TestWebhookMatchesAlias(t *testing.T) {
	srv, sh := webhookServer(t)
	if err := srv.st.AddAlias(sh.ID, "トム・レイダー王"); err != nil {
		t.Fatalf("alias: %v", err)
	}
	rec := post(t, srv, "/api/webhook", `{"show":"トム・レイダー王","episode":9}`)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	ep, _ := srv.st.GetEpisode(sh.ID, 9)
	if ep == nil || ep.State != episode.Watched {
		t.Errorf("episode not marked watched: %+v", ep)
	}
}

// TestWebhookRefusesNoIdentity: a payload with neither a path nor a name plus
// episode carries nothing to act on, and must say so rather than guess.
func TestWebhookRefusesNoIdentity(t *testing.T) {
	srv, _ := webhookServer(t)
	rec := post(t, srv, "/api/webhook", `{"Event":"item.markplayed"}`)
	if rec.Code != 422 {
		t.Errorf("status %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

// TestWebhookPathMatchesLikeWatched: a path through the webhook resolves the
// same way as through /api/watched — the two intakes must agree on what a
// filename means.
func TestWebhookPathMatchesLikeWatched(t *testing.T) {
	srv, sh := webhookServer(t)
	body := `{"path":"D:\\Anime\\[ToonsHub] Tomb Raider King S01E09 1080p WEB-DL.mkv"}`
	rec := post(t, srv, "/api/webhook", body)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	ep, _ := srv.st.GetEpisode(sh.ID, 9)
	if ep == nil || ep.State != episode.Watched {
		t.Errorf("episode not marked watched: %+v", ep)
	}
}
