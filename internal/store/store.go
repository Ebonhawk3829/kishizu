// Package store persists shows, aliases, offsets, filters, preferences and
// episode state in SQLite.
package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

// Store wraps the database.
type Store struct {
	db *sql.DB
}

// Exec runs a raw SQL statement. Exported for tests in other packages that
// need to arrange state the store's own methods do not model (backdating a
// timestamp, for instance); production code should use the typed methods.
func (s *Store) Exec(query string, args ...any) (sql.Result, error) {
	return s.db.Exec(query, args...)
}

// Open opens (or creates) the database at path and applies the schema.
//
// busy_timeout matters: the poller and the UI both write, and SQLite's default
// behaviour on contention is to fail immediately rather than wait.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// SQLite is a single-writer database; more connections only create
	// contention. dig does the same.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// timeLayouts covers the formats SQLite can hand back for a datetime column.
// modernc.org/sqlite returns datetime('now') as a TEXT string, not a time.Time,
// so every datetime column has to be parsed rather than scanned directly.
var timeLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02 15:04:05.999999999-07:00",
	time.RFC3339,
}

func parseTime(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, v.String); err == nil {
			return &t
		}
	}
	return nil
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// migrate brings a database up to the current schema version.
//
// schema.sql creates everything for a fresh database. For an existing one it
// runs verbatim (every statement is IF NOT EXISTS), then versioned migration
// steps run in order — each step is idempotent and recorded in schema_version,
// so a partially-migrated database resumes where it left off.
func (s *Store) migrate() error {
	b, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("read schema: %w", err)
	}
	if _, err := s.db.Exec(string(b)); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	// Runs after the schema so columns added since this database was created
	// are present before anything selects them.
	if err := s.addColumns(); err != nil {
		return err
	}
	return s.migrateSteps()
}

// migrateSteps applies each recorded version step that has not run yet.
func (s *Store) migrateSteps() error {
	type step struct {
		version int
		what    string
		run     func(*Store) error
	}
	steps := []step{
		{1, "drop superseded filter/rejected tables", func(s *Store) error {
			// Release quality policy is global and hardcoded
			// (quality.Default); the per-show rule tables were
			// superseded and nothing reads them. The rejected table
			// was write-only.
			//
			// "preference" stays: the schema creates it for
			// UI-owned settings, and this step runs after
			// the schema, so dropping it here would delete the table on
			// every fresh database. A step that undoes the schema is a
			// step that can never be correct.
			for _, t := range []string{"filter", "rejected"} {
				if _, err := s.db.Exec(`DROP TABLE IF EXISTS ` + t); err != nil {
					return fmt.Errorf("drop %s: %w", t, err)
				}
			}
			return nil
		}},
		{2, "drop per-episode air dates and cadence columns", func(s *Store) error {
			// The schedule's countdown is the only air-date fact anyone
			// needs: the next unaired episode is next_ep and it airs at
			// next_airs_at. Per-episode projections simulated what the site
			// publishes, and every deviation from weekly cadence (hiatus,
			// delay, special) corrupted them into phantom dashboard entries.
			// The cadence columns were never written by any code path.
			//
			// SQLite cannot DROP COLUMN before 3.35, so the tables are
			// rebuilt. Every column except the dropped ones is copied
			// verbatim, so episode history and show identity survive intact.
			//
			// The rebuild runs with foreign_keys OFF. With it ON, DROP TABLE
			// on a parent performs an implicit DELETE FROM that fires ON
			// DELETE CASCADE in every child still referencing it: dropping
			// the old show table cascade-deletes every episode, alias and
			// group-offset row before they can be copied. The pragma is
			// per-connection and the pool holds a single connection, so the
			// OFF here cannot leak into other callers. FK enforcement is
			// restored before the step returns, and the final foreign_key_check
			// proves the rebuilt tables are consistent — the OFF window is
			// for DDL mechanics only, never for writing through a constraint.
			if _, err := s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
				return fmt.Errorf("disable foreign keys: %w", err)
			}
			defer func() {
				if _, err := s.db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
					// Enforcement must come back on: leaving it off would
					// let orphan rows accumulate silently for the process
					// lifetime. Nothing below can run if this fails.
					panic(fmt.Sprintf("restore foreign keys: %v", err))
				}
			}()
			for _, stmt := range []string{
				`CREATE TABLE show_new (
					id             INTEGER PRIMARY KEY AUTOINCREMENT,
					canonical_name TEXT    NOT NULL UNIQUE,
					max_episode    INTEGER NOT NULL DEFAULT 0,
					source         TEXT    NOT NULL DEFAULT 'manual',
					next_ep         INTEGER,
					next_airs_at    TEXT,
					schedule_fetched_at TEXT,
					image_url       TEXT,
					slug            TEXT,
					airing_status   TEXT,
					created_at     TEXT    NOT NULL DEFAULT (datetime('now'))
				)`,
				`INSERT INTO show_new
					SELECT id, canonical_name, max_episode, source, next_ep, next_airs_at, schedule_fetched_at, image_url, slug, airing_status, created_at FROM show`,
				`DROP TABLE show`,
				`ALTER TABLE show_new RENAME TO show`,
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_show_slug ON show(slug) WHERE slug IS NOT NULL`,
				`CREATE TABLE episode_new (
					show_id       INTEGER NOT NULL REFERENCES show(id) ON DELETE CASCADE,
					number        INTEGER NOT NULL,
					state         TEXT    NOT NULL DEFAULT 'wanted',
					infohash      TEXT,
					release_title TEXT,
					file_path     TEXT,
					downloaded_at TEXT,
					watched_at    TEXT,
					exposed_at    TEXT,
					PRIMARY KEY (show_id, number)
				)`,
				`INSERT INTO episode_new
					SELECT show_id, number, state, infohash, release_title, file_path, downloaded_at, watched_at, exposed_at FROM episode`,
				`DROP TABLE episode`,
				`ALTER TABLE episode_new RENAME TO episode`,
				`CREATE INDEX IF NOT EXISTS idx_episode_state ON episode(state)`,
				// The rebuild is only safe to hand back with proof that no
				// child row lost its parent along the way. A violation here
				// means the copy itself was wrong, not the pragma window.
				`PRAGMA foreign_key_check`,
			} {
				if _, err := s.db.Exec(stmt); err != nil {
					return fmt.Errorf("exec: %w", err)
				}
			}
			return nil
		}},
		{3, "backfill seen.origin for rows written before the column existed", func(s *Store) error {
			// Every pre-column row was written by the listener's grab path or
			// the adopt path — training only started writing here when the
			// origin column arrived. Defaulting to 'grab' preserves exactly
			// the behaviour those rows had before the split: the listener
			// skips them, training skips them (they were grabbed, so there
			// is nothing new to learn).
			if _, err := s.db.Exec(`UPDATE seen SET origin = 'grab' WHERE origin IS NULL OR origin = ''`); err != nil {
				return fmt.Errorf("backfill origin: %w", err)
			}
			return nil
		}},
	}
	for _, st := range steps {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = ?`, st.version).Scan(&n); err != nil {
			return fmt.Errorf("check schema version %d: %w", st.version, err)
		}
		if n > 0 {
			continue
		}
		if err := st.run(s); err != nil {
			return fmt.Errorf("migration %d (%s): %w", st.version, st.what, err)
		}
		if _, err := s.db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, st.version); err != nil {
			return fmt.Errorf("record schema version %d: %w", st.version, err)
		}
	}
	return nil
}

// addColumns brings an existing database up to date.
//
// CREATE TABLE IF NOT EXISTS silently skips tables that already exist, so a
// column added later never lands on an older database. Every additive schema
// change needs an explicit ALTER here.
func (s *Store) addColumns() error {
	type col struct{ table, name, def string }
	cols := []col{
		{"show", "next_ep", "INTEGER"},
		{"show", "next_airs_at", "TEXT"},
		{"show", "schedule_fetched_at", "TEXT"},
		{"show", "image_url", "TEXT"},
		{"show", "slug", "TEXT"},
		{"show", "airing_status", "TEXT"},
		{"alias", "source", "TEXT NOT NULL DEFAULT 'manual'"},
		{"seen", "origin", "TEXT NOT NULL DEFAULT 'grab'"},
		{"episode", "exposed_at", "TEXT"},
	}
	for _, c := range cols {
		rows, err := s.db.Query(
			`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, c.table, c.name)
		if err != nil {
			return fmt.Errorf("check column %s.%s: %w", c.table, c.name, err)
		}
		var n int
		if rows.Next() {
			_ = rows.Scan(&n)
		}
		rows.Close()
		if n > 0 {
			continue
		}
		if _, err := s.db.Exec(
			fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, c.table, c.name, c.def)); err != nil {
			return fmt.Errorf("add column %s.%s: %w", c.table, c.name, err)
		}
	}
	return nil
}

// ---------- shows ----------

// Show is a tracked show with everything the matcher needs.
type Show struct {
	ID            int64
	CanonicalName string
	MaxEpisode    int
	Source        string
	// ImageURL is the season's cover art from the schedule, for the UI.
	ImageURL string
	// Slug is the animeschedule.net slug, an exact identity for the show on
	// the schedule. Empty when the show has no schedule page.
	Slug string
	// AiringStatus is the schedule page's own Status field: Ongoing, Finished,
	// Upcoming. Empty when never fetched. This is the season-complete signal:
	// the page says so directly, which absence of a countdown cannot (a show
	// on hiatus has no countdown either).
	AiringStatus string
	CreatedAt    time.Time
	Aliases      []string
}

// CreateShow inserts a show with its aliases. The canonical name is always
// stored as an alias too, so matching never has to special-case it.
func (s *Store) CreateShow(canonical string, aliases []string, maxEpisode int) (*Show, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO show (canonical_name, max_episode) VALUES (?, ?)`, canonical, maxEpisode)
	if err != nil {
		return nil, fmt.Errorf("insert show: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}

	all := append([]string{canonical}, aliases...)
	seen := map[string]bool{}
	for _, a := range all {
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		if _, err := tx.Exec(`INSERT OR IGNORE INTO alias (show_id, name) VALUES (?, ?)`, id, a); err != nil {
			return nil, fmt.Errorf("insert alias %q: %w", a, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetShow(id)
}

// GetShow loads a show and its aliases.
func (s *Store) GetShow(id int64) (*Show, error) {
	row := s.db.QueryRow(`SELECT id, canonical_name, max_episode, source,
		image_url, slug, airing_status, created_at FROM show WHERE id = ?`, id)

	var sh Show
	var source, created, image, slug, airing sql.NullString
	if err := row.Scan(&sh.ID, &sh.CanonicalName, &sh.MaxEpisode, &source,
		&image, &slug, &airing, &created); err != nil {
		return nil, err
	}
	sh.Slug = slug.String
	sh.AiringStatus = airing.String
	sh.Source = source.String
	sh.ImageURL = image.String
	sh.CreatedAt = derefTime(parseTime(created))

	rows, err := s.db.Query(`SELECT name FROM alias WHERE show_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		sh.Aliases = append(sh.Aliases, a)
	}
	return &sh, rows.Err()
}

// ListShows returns every tracked show.
func (s *Store) ListShows() ([]*Show, error) {
	rows, err := s.db.Query(`SELECT id FROM show ORDER BY canonical_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]*Show, 0, len(ids))
	for _, id := range ids {
		sh, err := s.GetShow(id)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, nil
}

// AddAlias records another title for a show. Idempotent.
//
// Source is provenance: manual | schedule | training | abbreviation. It is
// recorded but not read back by the matcher, which treats every alias the
// same. It exists so a future UI can show where a name came from, and so a
// schedule-derived alias the user deleted can be told apart from one they
// typed.
func (s *Store) AddAlias(showID int64, alias string) error {
	return s.AddAliasFrom(showID, alias, "manual")
}

// AddAliasFrom records a title with an explicit provenance.
func (s *Store) AddAliasFrom(showID int64, alias, source string) error {
	if alias == "" {
		return nil
	}
	if source == "" {
		source = "manual"
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO alias (show_id, name, source) VALUES (?, ?, ?)`,
		showID, alias, source)
	return err
}

// LearnVocabulary records that a title token means a canonical value.
//
// Both halves are required: the canonical value alone tells us the answer but
// not which word produced it, so nothing can be applied to the next release
// that uses the same spelling.
func (s *Store) LearnVocabulary(kind, token, canonical string) error {
	token = strings.TrimSpace(token)
	canonical = strings.TrimSpace(canonical)
	if token == "" || canonical == "" {
		return nil
	}
	_, err := s.db.Exec(
		`INSERT INTO vocabulary (kind, token, canonical) VALUES (?, ?, ?)
		 ON CONFLICT(kind, token) DO UPDATE SET canonical = excluded.canonical`,
		kind, token, strings.ToLower(canonical))
	return err
}

// SetPreference records a user preference.
//
// Preferences are UI-owned settings, as distinct from deployment
// configuration. They live in the database rather than the config file
// because the UI writes them, they are per-user rather than per-deployment,
// and writing them to the file meant a read-modify-write of a file the
// operator also edits by hand — with no locking, so a toggle could silently
// lose a concurrent edit.
func (s *Store) SetPreference(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO preference (key, value, updated_at) VALUES (?, ?, datetime('now'))
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value)
	return err
}

// Preference reads a user preference. found is false when it has never been
// set, which the caller treats as "use the default" rather than as an error:
// an unset preference is the normal state of a fresh install.
func (s *Store) Preference(key string) (value string, found bool, err error) {
	var v string
	err = s.db.QueryRow(`SELECT value FROM preference WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// Vocabulary loads every learned synonym, grouped by kind.
func (s *Store) Vocabulary() (map[string]map[string]string, error) {
	rows, err := s.db.Query(`SELECT kind, token, canonical FROM vocabulary`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]string{}
	for rows.Next() {
		var kind, token, canonical string
		if err := rows.Scan(&kind, &token, &canonical); err != nil {
			return nil, err
		}
		if out[kind] == nil {
			out[kind] = map[string]string{}
		}
		out[kind][token] = canonical
	}
	return out, rows.Err()
}

// SetSlug records the animeschedule.net slug for a show.
func (s *Store) SetSlug(showID int64, slug string) error {
	_, err := s.db.Exec(`UPDATE show SET slug = ? WHERE id = ?`, slug, showID)
	return err
}

// Where a show came from. Recorded on the show row and used to decide which
// questions apply to it.
const (
	// SourceManual is a show added by name, with no schedule identity.
	SourceManual = "manual"
	// SourceSchedule is a show added from an animeschedule.net URL. It has a
	// slug and an anchor, so the airing pipeline applies to it.
	SourceSchedule = "schedule"
	// SourceSeaDex is a finished season adopted from releases.moe. It has no
	// anchor and is never trained, so the airing pipeline must skip it and
	// the UI must not ask whether it has aired or needs training.
	SourceSeaDex = "seadex"
)

// SetSource records where a show came from.
func (s *Store) SetSource(showID int64, source string) error {
	_, err := s.db.Exec(`UPDATE show SET source = ? WHERE id = ?`, source, showID)
	return err
}

// SetMaxEpisode updates the plausibility bound used by the matcher.
func (s *Store) SetMaxEpisode(showID int64, max int) error {
	_, err := s.db.Exec(`UPDATE show SET max_episode = ? WHERE id = ?`, max, showID)
	return err
}

// GetShowByName finds a show by canonical name. Returns (nil, nil) when absent.
func (s *Store) GetShowByName(name string) (*Show, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM show WHERE canonical_name = ?`, name).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.GetShow(id)
}

// SetAiringStatus records the schedule page's Status field verbatim.
//
// Stored verbatim rather than reduced to a boolean: the page distinguishes
// Ongoing, Finished and Upcoming, and a future consumer may want any of them.
// Finished() is the opinionated read.
func (s *Store) SetAiringStatus(showID int64, status string) error {
	_, err := s.db.Exec(`UPDATE show SET airing_status = ? WHERE id = ?`,
		nullIfEmpty(strings.TrimSpace(status)), showID)
	return err
}

// Finished reports whether the schedule says the season has ended.
//
// Only an explicit "Finished" counts. An absent countdown is not evidence —
// a show on hiatus or awaiting a slot has no countdown either, which is the
// same reading the timetable's DropFinished applies.
func (sh *Show) Finished() bool {
	return strings.EqualFold(strings.TrimSpace(sh.AiringStatus), "Finished")
}

// SetNextEpisode records the schedule's authoritative next-episode point:
// episode n airs at t. This is the ONLY stored air-date fact, and the daily
// refresh is its only writer. Every consumer that needs to know when the next
// unaired episode airs reads it through NextEpisode; nothing else about the
// airing schedule is stored, because the site publishes exactly this and
// simulating more (projecting N+1/N+2, advancing on grab) is how phantom
// dashboard entries happen.
//
// n is clamped to at least 1. The page renders "Ep 0" for a show that has
// been announced but has not premiered, and storing that verbatim breaks
// everything downstream: episode numbers are 1-based, plausible() rejects
// anything below 1, and the season-complete check (next > max) can never fire
// for a next of 0. Treating it as episode 1 is the honest reading — the next
// episode is the first one — and the daily refresh corrects the time once the
// show actually appears on the timetable.
func (s *Store) SetNextEpisode(showID int64, n int, t time.Time) error {
	if n < 1 {
		n = 1
	}
	// Record the slot in the air cache as well as the anchor. The countdown
	// moves on and never mentions this episode again, so this is the one
	// chance to keep the fact; the cache is what lets the watch gate anchor
	// behind the site's cursor and still know when that older episode aired.
	if err := s.CacheAirTime(showID, n, t); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE show SET next_ep = ?, next_airs_at = ?,
		schedule_fetched_at = datetime('now') WHERE id = ?`,
		n, t.UTC().Format("2006-01-02 15:04:05"), showID)
	return err
}

// ClearNextEpisode removes the stored anchor entirely.
//
// A page with no countdown and no episode number is not "keep whatever we
// had" — it is the site saying it has no air information. Leaving a stale
// anchor in place freezes it: the refresh only writes when the page publishes
// a countdown, so a bogus row (an "Ep 0" premiere countdown captured mid-site-
// update) would otherwise never be corrected and the UI would go on reporting
// a premiere that the site itself no longer mentions. NULL is the honest
// value: the show reads as unscheduled until the page publishes a real one.
func (s *Store) ClearNextEpisode(showID int64) error {
	_, err := s.db.Exec(`UPDATE show SET next_ep = NULL, next_airs_at = NULL,
		schedule_fetched_at = datetime('now') WHERE id = ?`, showID)
	return err
}

// nullIfEmpty maps an empty string to SQL NULL, so "unknown" stays
// distinguishable from "known to be empty".
func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// SetImageURL records the season's cover art, scraped from the schedule.
// Empty string clears it, so a show that loses its art falls back cleanly.
func (s *Store) SetImageURL(showID int64, url string) error {
	_, err := s.db.Exec(`UPDATE show SET image_url = ? WHERE id = ?`, nullIfEmpty(url), showID)
	return err
}

// NextEpisode returns the schedule's next-episode point, if known.
// SiteNextEpisode returns the schedule's raw next-episode point: episode n
// airs at t, exactly as the site's countdown says. This is the fact the daily
// refresh writes, and the UI's air line renders it verbatim — it is true
// regardless of where kishizu's own work is up to.
//
// The listener and the cycle logic must NOT consume this directly: they read
// the effective anchor through NextEpisode, which caps it at the user's
// watching progress. Consuming the site's cursor directly is what made a show
// forget its unaired-but-released episodes the moment the countdown moved on.
func (s *Store) SiteNextEpisode(showID int64) (int, *time.Time, error) {
	var n sql.NullInt64
	var at sql.NullString
	err := s.db.QueryRow(`SELECT next_ep, next_airs_at FROM show WHERE id = ?`, showID).
		Scan(&n, &at)
	if err == sql.ErrNoRows {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	if !n.Valid || !at.Valid {
		return 0, nil, nil
	}
	t := parseTime(at)
	if t == nil {
		return int(n.Int64), nil, nil
	}
	return int(n.Int64), t, nil
}

// NextEpisode returns the show's effective anchor: the episode kishizu should
// act on next, and when it airs.
//
// The anchor is the earlier of two facts: the site's next episode (what the
// season is publishing) and the user's progress (the first episode without a
// watched or deleted signal). Whichever is behind wins, because an episode the
// user has not consumed is not done with — the site advertising ep5 while ep2
// sits unwatched does not make ep2 somebody else's problem.
//
// The air time follows the effective episode: the site's slot when the two
// coincide, otherwise the slot recorded in the air cache when the site
// published that episode back when it was "next". No cached slot means the
// time is unknown and the episode hunts immediately — the honest reading of
// "this aired, and we never saw when".
//
// When the gate exposes an episode long after its air window closed (a
// catch-up binge: the site is at ep8 and the user just watched ep1), the
// cached air time would place the episode's 72-hour hunting window in the
// past before the listener ever looked. The exposure stamp fixes that: the
// consume paths stamp the newly exposed episode at the moment the gate
// advanced to it, and the effective time is the LATER of air time and
// exposure. The window then measures real opportunity to grab — 72 hours of
// actual hunting from exposure — instead of a deadline that expired before
// the episode was huntable.
//
// Progress is max-based (see NextUnwatched): a gap in watched history is a
// failed watch signal, not out-of-order viewing, so the gate never skips ahead
// of a hole.
func (s *Store) NextEpisode(showID int64) (int, *time.Time, error) {
	siteN, siteAt, err := s.SiteNextEpisode(showID)
	if err != nil {
		return 0, nil, err
	}
	progress := s.NextUnwatched(showID)

	n, at := siteN, siteAt
	if progress < n || n <= 0 {
		// The user is behind the site's cursor: the effective episode is the
		// first unconsumed one, and its air time comes from the cache — the
		// site published that slot when the episode was next, and the refresh
		// has since moved the countdown past it.
		n = progress
		at = s.cachedAirTime(showID, n)
		// The window opens no earlier than exposure: a stale air time must
		// not close a window that never got to open.
		if exp := s.exposureTime(showID, n); exp != nil && (at == nil || exp.After(*at)) {
			at = exp
		}
	}
	if n <= 0 {
		return 0, nil, nil
	}
	return n, at, nil
}

// exposureTime reads when the gate exposed an episode to hunting. nil when
// the episode has no row or no stamp — the window then runs from the air
// time alone.
func (s *Store) exposureTime(showID int64, episode int) *time.Time {
	var at sql.NullString
	err := s.db.QueryRow(`SELECT exposed_at FROM episode WHERE show_id = ? AND number = ?`,
		showID, episode).Scan(&at)
	if err != nil {
		return nil
	}
	return parseTime(at)
}

// cachedAirTime reads one episode's slot from the air cache. nil when the
// cache has no row — the caller treats that as "aired, time unknown".
func (s *Store) cachedAirTime(showID int64, episode int) *time.Time {
	var at sql.NullString
	err := s.db.QueryRow(`SELECT airs_at FROM air_cache WHERE show_id = ? AND episode = ?`,
		showID, episode).Scan(&at)
	if err != nil {
		return nil
	}
	return parseTime(at)
}

// CacheAirTime records a slot the schedule published: episode n airs at t.
// The daily refresh is the only writer; a row is a fact, never an inference.
func (s *Store) CacheAirTime(showID int64, n int, t time.Time) error {
	if n < 1 || t.IsZero() {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO air_cache (show_id, episode, airs_at) VALUES (?, ?, ?)
		ON CONFLICT(show_id, episode) DO UPDATE SET airs_at = excluded.airs_at`,
		showID, n, t.UTC().Format("2006-01-02 15:04:05"))
	return err
}

// ClearAirCache drops the cached slots for episodes 1..n. A watch or delete
// signal calls this: consumed episodes have no future, so their slots are
// dead weight the cache should not carry.
func (s *Store) ClearAirCache(showID int64, upTo int) error {
	if upTo < 1 {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM air_cache WHERE show_id = ? AND episode <= ?`, showID, upTo)
	return err
}

// StampExposure records the moment the gate exposed an episode to hunting.
//
// A consume signal (watched or deleted) advances the effective anchor to the
// next unconsumed episode. If that episode's air window already closed — a
// catch-up binge, where the site sits episodes ahead of the user — its
// 72-hour window would expire before the listener ever looked at it. The
// exposure stamp is the fix: the window opens no earlier than this moment,
// so it measures real hunting time.
//
// The stamp lives on a wanted episode row, created here if absent. A wanted
// row is inert everywhere else — the resurrection guard only stops terminal
// states, NextUnwatched counts only watched/deleted, and the UI renders
// wanted as the anchor's state — so creating one has no side effects beyond
// carrying the stamp.
//
// Re-watching an already-consumed episode refreshes the stamp, extending the
// window 72 hours. Harmless: the episode is consumed either way, and the
// gate does not move.
func (s *Store) StampExposure(showID int64, episode int) error {
	if episode < 1 {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO episode (show_id, number, state, exposed_at)
		VALUES (?, ?, 'wanted', datetime('now'))
		ON CONFLICT(show_id, number) DO UPDATE SET exposed_at = datetime('now')`,
		showID, episode)
	return err
}

// DeleteShow removes a show and everything hanging off it (cascade).
//
// Child tables declare ON DELETE CASCADE, so aliases, offsets, filters,
// preferences, episodes and rejections go with it. `seen` has no foreign key,
// so it is cleared explicitly: leftover rows would keep dedupe entries alive
// for a show that no longer exists.
func (s *Store) DeleteShow(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM seen WHERE show_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM show WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------- group offsets ----------

// SetGroupOffset records the offset for a group. Offsets are per group because
// groups disagree about numbering within one show.
func (s *Store) SetGroupOffset(showID int64, group string, offset int, learnedFrom string) error {
	if group == "" {
		group = "(none)"
	}
	_, err := s.db.Exec(`INSERT INTO group_offset (show_id, group_name, offset_value, learned_from)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (show_id, group_name) DO UPDATE SET offset_value = excluded.offset_value,
			learned_from = excluded.learned_from`, showID, group, offset, learnedFrom)
	return err
}

// ClearGroupOffsets removes every learned offset for a show, so training can
// start from a clean slate.
//
// Needed because a bad training run leaves offsets that look authoritative but
// are wrong, and there is no other way to undo them.
func (s *Store) ClearGroupOffsets(showID int64) error {
	_, err := s.db.Exec(`DELETE FROM group_offset WHERE show_id = ?`, showID)
	return err
}

// GroupOffsets returns every known offset for a show.
func (s *Store) GroupOffsets(showID int64) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT group_name, offset_value FROM group_offset WHERE show_id = ?`, showID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var g string
		var v int
		if err := rows.Scan(&g, &v); err != nil {
			return nil, err
		}
		out[g] = v
	}
	return out, rows.Err()
}

// ---------- seen infohashes ----------

// SeenOrigin records who wrote a seen row. The two consumers of the table ask
// different questions, and a row from one must never answer the other's:
//
//	SeenGrab  — the listener downloaded it (or an adoption did). The listener
//	            skips these: never re-grab. Training skips them too — a
//	            downloaded release has nothing left to teach.
//	SeenTrain — training confirmed it. Training skips these: do not re-offer.
//	            The listener must NOT skip them — a confirmed release is
//	            exactly the one that should be grabbed.
type SeenOrigin string

const (
	SeenGrab  SeenOrigin = "grab"
	SeenTrain SeenOrigin = "train"
)

// MarkSeen records an infohash we have acted on, tagged with who acted.
// Persisted separately from episode rows so a release that reappears after
// its episode is deleted is still recognised.
func (s *Store) MarkSeen(infohash string, showID int64, episode int, origin SeenOrigin) error {
	if infohash == "" {
		return nil
	}
	if origin == "" {
		origin = SeenGrab
	}
	_, err := s.db.Exec(`INSERT INTO seen (infohash, show_id, episode, origin)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(infohash) DO UPDATE SET origin = excluded.origin`,
		infohash, showID, episode, string(origin))
	return err
}

// HasSeen reports whether we have acted on this infohash before, from the
// given consumer's perspective.
//
// The origin split is the point. A training confirm and a listener grab both
// write here, but they mean different things: training must not re-offer a
// confirmed release, while the listener must happily grab one. Reading all
// origins made a training confirm permanently block the grab — the bug that
// left Black Clover hunting forever.
func (s *Store) HasSeen(infohash string, origin SeenOrigin) (bool, error) {
	if infohash == "" {
		return false, nil
	}
	var n int
	err := s.db.QueryRow(`SELECT 1 FROM seen WHERE infohash = ? AND origin = ?`,
		infohash, string(origin)).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
