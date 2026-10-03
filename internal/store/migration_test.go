package store

import (
	"testing"

	"github.com/Ebonhawk3829/kishizu/internal/episode"
)

// A v1.3.9 database has the pre-refactor shapes: episode carries airs_at and
// show carries the cadence_* columns. Migration step 2 rebuilds both tables,
// and the rebuild must not lose child rows.
func v139Schema(t *testing.T) *Store {
	t.Helper()
	s := testStore(t)
	for _, stmt := range []string{
		// The v1.3.9 shapes, restored by adding the columns the current
		// schema no longer has. A fresh test database never has them, so
		// migration step 2 has nothing to do on it — which is exactly why
		// the wipe this test guards against passed every existing test.
		`ALTER TABLE episode ADD COLUMN airs_at TEXT`,
		`ALTER TABLE show ADD COLUMN cadence_weekday INTEGER`,
		`ALTER TABLE show ADD COLUMN cadence_source TEXT`,
		`ALTER TABLE show ADD COLUMN cadence_fetched_at TEXT`,
		`DELETE FROM schema_version WHERE version = 2`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("seed v1.3.9 schema: %v", err)
		}
	}
	return s
}

// TestMigrationStep2KeepsChildRows guards the rebuild order in migration
// step 2. The connection runs with foreign_keys ON, and DROP TABLE on a
// parent fires ON DELETE CASCADE in every child still referencing it. The
// first version of step 2 rebuilt episode before show, so DROP TABLE show
// cascade-deleted every episode, alias and group-offset row on every
// database upgraded from v1.3.9 — silently, and invisible to tests because
// fresh test databases have no v1.3.9 columns for the step to act on.
func TestMigrationStep2KeepsChildRows(t *testing.T) {
	s := v139Schema(t)

	sh, err := s.CreateShow("Test Show", []string{"Test Alias"}, 12)
	if err != nil {
		t.Fatalf("CreateShow: %v", err)
	}
	if err := s.UpsertEpisode(sh.ID, 1, episode.Watched, "", ""); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	if err := s.SetGroupOffset(sh.ID, "SubsPlease", 0, "training"); err != nil {
		t.Fatalf("SetGroupOffset: %v", err)
	}
	if err := s.migrateSteps(); err != nil {
		t.Fatalf("migrateSteps: %v", err)
	}

	var eps, aliases, offsets int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM episode`).Scan(&eps); err != nil {
		t.Fatalf("count episode: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM alias`).Scan(&aliases); err != nil {
		t.Fatalf("count alias: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM group_offset`).Scan(&offsets); err != nil {
		t.Fatalf("count group_offset: %v", err)
	}
	if eps != 1 {
		t.Errorf("episode rows = %d, want 1 (cascade wiped them)", eps)
	}
	if aliases < 2 {
		t.Errorf("alias rows = %d, want at least 2 (cascade wiped them)", aliases)
	}
	if offsets != 1 {
		t.Errorf("group_offset rows = %d, want 1 (cascade wiped them)", offsets)
	}

	// The rebuilt episode table must also still work: the state survived
	// the copy verbatim.
	ep, err := s.GetEpisode(sh.ID, 1)
	if err != nil {
		t.Fatalf("Episode after migration: %v", err)
	}
	if ep.State != episode.Watched {
		t.Errorf("state = %q, want watched", ep.State)
	}
}
