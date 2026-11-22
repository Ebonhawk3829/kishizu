package web

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
	"github.com/Ebonhawk3829/kishizu/internal/store"
	"github.com/Ebonhawk3829/kishizu/internal/watch"
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

	sh, _ := st.CreateShow("Show", []string{"Alt Name"})
	_ = st.SetGroupOffset(sh.ID, "VARYG", 40, "test")
	_ = st.UpsertEpisode(sh.ID, 1, episode.Watched, "", "")
	_ = st.MarkSeen("deadbeef", sh.ID, 1, store.SeenGrab)

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
	again, err := st.CreateShow("Show", nil)
	if err != nil {
		t.Fatal(err)
	}
	if eps, _ := st.EpisodesForShow(again.ID); len(eps) != 0 {
		t.Errorf("%d episodes inherited by the re-created show", len(eps))
	}
	if offs, _ := st.GroupOffsets(again.ID); len(offs) != 0 {
		t.Errorf("%d offsets inherited by the re-created show", len(offs))
	}
	if seen, _ := st.HasSeen("deadbeef", store.SeenGrab); seen {
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

// TestDeleteShowDeletesFiles: the caller may ask for the show's episodes to
// be removed from disk as part of the removal. The files go, the show row
// goes, and the response reports how many were deleted.
func TestDeleteShowDeletesFiles(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "library")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	sh, _ := st.CreateShow("Show", nil)
	if err := st.UpsertEpisode(sh.ID, 1, episode.Downloaded, "", ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lib, "Show - 01.mkv")
	if err := os.WriteFile(path, []byte("episode"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFilePath(sh.ID, 1, path); err != nil {
		t.Fatal(err)
	}

	s := &Server{st: st, watch: watch.New(st, lib, 0, watch.DeleteOff, 0)}
	body := fmt.Sprintf(`{"id":%d,"delete_files":true}`, sh.ID)
	req := httptest.NewRequest("DELETE", "/api/shows", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("delete: status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		FilesDeleted int `json:"files_deleted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.FilesDeleted != 1 {
		t.Errorf("files_deleted = %d, want 1", resp.FilesDeleted)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("episode file survived: %v", err)
	}
	if got, _ := st.GetShow(sh.ID); got != nil {
		t.Error("show still present after delete")
	}
}

// TestDeleteShowKeepsFilesByDefault: without the opt-in the episodes are left
// alone. Removing a show stops tracking; it does not clean the disk.
func TestDeleteShowKeepsFilesByDefault(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "library")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	sh, _ := st.CreateShow("Show", nil)
	if err := st.UpsertEpisode(sh.ID, 1, episode.Downloaded, "", ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lib, "Show - 01.mkv")
	if err := os.WriteFile(path, []byte("episode"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFilePath(sh.ID, 1, path); err != nil {
		t.Fatal(err)
	}

	s := &Server{st: st, watch: watch.New(st, lib, 0, watch.DeleteOff, 0)}
	body := fmt.Sprintf(`{"id":%d}`, sh.ID)
	req := httptest.NewRequest("DELETE", "/api/shows", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("delete: status %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("episode file should survive a default removal: %v", err)
	}
}

// TestDeleteShowFilesRequiresLibrary: asking to delete files when no library
// is configured is a 400 and leaves the show in place, because there is
// nowhere safe to delete from.
func TestDeleteShowFilesRequiresLibrary(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	sh, _ := st.CreateShow("Show", nil)
	s := &Server{st: st}
	body := fmt.Sprintf(`{"id":%d,"delete_files":true}`, sh.ID)
	req := httptest.NewRequest("DELETE", "/api/shows", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if got, _ := st.GetShow(sh.ID); got == nil {
		t.Error("show was removed despite the failed request")
	}
}
