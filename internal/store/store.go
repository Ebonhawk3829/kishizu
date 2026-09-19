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

	"github.com/Ebonhawk3829/kishizu/internal/episode"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

// Store wraps the database.
type Store struct {
	db *sql.DB
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
		db.Close()
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
		{1, "drop superseded filter/preference/rejected tables", func(s *Store) error {
			// Release quality policy is global and hardcoded (rules.go);
			// the per-show rule tables were superseded and nothing reads
			// them. The rejected table was write-only.
			for _, t := range []string{"filter", "preference", "rejected"} {
				if _, err := s.db.Exec(`DROP TABLE IF EXISTS ` + t); err != nil {
					return fmt.Errorf("drop %s: %w", t, err)
				}
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
		{"alias", "source", "TEXT NOT NULL DEFAULT 'manual'"},
		{"episode", "airs_at", "TEXT"},
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

// ---------- seen infohashes ----------

// ---------- shows ----------

// Show is a tracked show with everything the matcher needs.
type Show struct {
	ID             int64
	CanonicalName  string
	MaxEpisode     int
	Source         string
	CadenceWeekday *int
	CadenceSource  string
	CadenceFetched *time.Time
	// ImageURL is the season's cover art from the schedule, for the UI.
	ImageURL string
	// Slug is the animeschedule.net slug, an exact identity for the show on
	// the schedule. Empty when the show has no schedule page.
	Slug      string
	CreatedAt time.Time
	Aliases   []string
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
		cadence_weekday, cadence_source, cadence_fetched_at, image_url, slug, created_at FROM show WHERE id = ?`, id)

	var sh Show
	var weekday sql.NullInt64
	var src, source, fetched, created, image, slug sql.NullString
	if err := row.Scan(&sh.ID, &sh.CanonicalName, &sh.MaxEpisode, &source,
		&weekday, &src, &fetched, &image, &slug, &created); err != nil {
		return nil, err
	}
	sh.Slug = slug.String
	if weekday.Valid {
		w := int(weekday.Int64)
		sh.CadenceWeekday = &w
	}
	sh.Source = source.String
	sh.CadenceSource = src.String
	sh.CadenceFetched = parseTime(fetched)
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
// Both halves are required. The canonical value alone tells us the answer but
// not which word produced it, so there would be nothing to apply to the next
// release that uses the same spelling.
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

// ShowBySlug looks a show up by its animeschedule slug. Returns nil when no
// show carries it.
func (s *Store) ShowBySlug(slug string) (*Show, error) {
	if slug == "" {
		return nil, nil
	}
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM show WHERE slug = ?`, slug).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return s.GetShow(id)
}

// Where a show came from. Recorded on the show row and used to decide which
// questions apply to it.
const (
	// SourceManual is a show added by name, with no schedule identity.
	SourceManual = "manual"
	// SourceSchedule is a show added from an animeschedule.net URL. It has a
	// slug and air dates, so the airing pipeline applies to it.
	SourceSchedule = "schedule"
	// SourceSeaDex is a finished season adopted from releases.moe. It has no
	// air dates and is never trained, so the airing pipeline must skip it and
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

// SetNextEpisode records the schedule's authoritative next-episode point:
// episode n airs at t. This is the one fact animeschedule.net gives us, and it
// is held until a download confirms the episode is real.
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
	_, err := s.db.Exec(`UPDATE show SET next_ep = ?, next_airs_at = ?,
		schedule_fetched_at = datetime('now') WHERE id = ?`,
		n, t.UTC().Format("2006-01-02 15:04:05"), showID)
	return err
}

// SetImageURL records the season's cover art, scraped from the schedule.
// Empty string clears it, so a show that loses its art falls back cleanly.
// nullIfEmpty keeps an absent value NULL rather than storing "", so "unknown"
// stays distinguishable from "known to be empty".
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
func (s *Store) NextEpisode(showID int64) (int, *time.Time, error) {
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

// ProjectAirDates fills in airs_at for episodes around the schedule's next
// episode, by stepping a week at a time from the known air time.
//
// The schedule only exposes the NEXT episode's timestamp, but the cadence is
// weekly, so ep n-1 aired seven days earlier, and so on.
//
// It also projects FORWARD when watch progress has passed the schedule point.
// If the user has watched ep 11 but the schedule still says "ep 11 airs Sep 6",
// then ep 12 is the one actually due — and without a forward projection it has
// no row and no air date, so it is invisible to the cycle and never hunted.
func (s *Store) ProjectAirDates(showID int64) error {
	n, at, err := s.NextEpisode(showID)
	if err != nil || at == nil || n < 1 {
		return err
	}

	// How far ahead of the schedule point has the user watched?
	watched := 0
	if eps, err := s.EpisodesForShow(showID); err == nil {
		for _, ep := range eps {
			if ep.State == episode.Watched || ep.State == episode.Deleted {
				if ep.Number > watched {
					watched = ep.Number
				}
			}
		}
	}
	// Project at least one episode beyond what has been watched, so the next
	// due episode always has an air date.
	target := n
	if watched+1 > target {
		target = watched + 1
	}

	for i := 1; i <= target; i++ {
		airs := at.AddDate(0, 0, 7*(i-n))
		if err := s.setAirsAt(showID, i, airs); err != nil {
			return err
		}
	}
	return nil
}

// setAirsAt records an episode's expected air time, creating the episode row as
// wanted if it does not exist yet.
func (s *Store) setAirsAt(showID int64, number int, t time.Time) error {
	ep, err := s.GetEpisode(showID, number)
	if err != nil {
		return err
	}
	stamp := t.UTC().Format("2006-01-02 15:04:05")
	if ep == nil {
		_, err := s.db.Exec(`INSERT INTO episode (show_id, number, state, airs_at)
			VALUES (?, ?, 'wanted', ?)`, showID, number, stamp)
		return err
	}
	_, err = s.db.Exec(`UPDATE episode SET airs_at = ? WHERE show_id = ? AND number = ?`,
		stamp, showID, number)
	return err
}

// AdvanceSchedule moves the schedule's pointer forward when episode n is
// grabbed: next_ep becomes n+1 and the air time projects forward a week.
//
// Called when a download is confirmed, per the contract: the schedule point is
// held until a download confirms that episode is real.
func (s *Store) AdvanceSchedule(showID int64, confirmedEp int) error {
	n, at, err := s.NextEpisode(showID)
	if err != nil || at == nil || n == 0 {
		return err
	}
	if confirmedEp < n {
		// An older episode was grabbed (backfill); the pointer stays.
		return nil
	}
	next := confirmedEp + 1
	nextAt := at.AddDate(0, 0, 7*(next-n))
	_, err = s.db.Exec(`UPDATE show SET next_ep = ?, next_airs_at = ? WHERE id = ?`,
		next, nextAt.UTC().Format("2006-01-02 15:04:05"), showID)
	return err
}

// DeleteShow removes a show and everything hanging off it (cascade).
//
// Child tables declare ON DELETE CASCADE, so aliases, offsets, filters,
// preferences, episodes and rejections go with it. `seen` has no foreign key,
// so it is cleared explicitly — leaving rows behind would keep dedupe entries
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

// ---------- group offsets ----------

// MarkSeen records an infohash we have acted on. Persisted separately from
// episode rows so a release that reappears after its episode is deleted is
// still recognised.
func (s *Store) MarkSeen(infohash string, showID int64, episode int) error {
	if infohash == "" {
		return nil
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO seen (infohash, show_id, episode) VALUES (?, ?, ?)`,
		infohash, showID, episode)
	return err
}

// HasSeen reports whether we have acted on this infohash before.
func (s *Store) HasSeen(infohash string) (bool, error) {
	if infohash == "" {
		return false, nil
	}
	var n int
	err := s.db.QueryRow(`SELECT 1 FROM seen WHERE infohash = ?`, infohash).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
