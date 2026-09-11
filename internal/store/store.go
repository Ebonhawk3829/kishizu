// Package store persists shows, aliases, offsets, filters, preferences and
// episode state in SQLite.
package store

import (
	"database/sql"
	"embed"
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

func (s *Store) migrate() error {
	b, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("read schema: %w", err)
	}
	if _, err := s.db.Exec(string(b)); err != nil {
		// Older databases hold duplicate rows that the new unique indexes
		// reject. Clean them and retry rather than failing to open.
		if err := s.dedupe(); err != nil {
			return err
		}
		if _, err := s.db.Exec(string(b)); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
	}
	// Runs after the schema so columns added since this database was created
	// are present before anything selects them.
	return s.addColumns()
}

// addColumns brings an existing database up to date.
//
// CREATE TABLE IF NOT EXISTS silently skips tables that already exist, so a
// column added later never lands on an older database. Every additive schema
// change needs an explicit ALTER here.
func (s *Store) addColumns() error {
	type col struct{ table, name, def string }
	cols := []col{
		{"filter", "reason", "TEXT"},
		{"preference", "reason", "TEXT"},
		{"show", "next_ep", "INTEGER"},
		{"show", "next_airs_at", "TEXT"},
		{"show", "schedule_fetched_at", "TEXT"},
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

// dedupe removes repeated filter/preference rows.
//
// Needed because these tables were originally plain INSERTs, so rejecting the
// same thing twice produced duplicates. The unique indexes added later cannot
// be created while duplicates exist, so old databases must be cleaned first.
func (s *Store) dedupe() error {
	stmts := []string{
		`DELETE FROM filter WHERE id NOT IN (
			SELECT MIN(id) FROM filter GROUP BY show_id, kind, op, value)`,
		`DELETE FROM preference WHERE id NOT IN (
			SELECT MIN(id) FROM preference GROUP BY show_id, kind, value)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("dedupe: %w", err)
		}
	}
	return nil
}

// ---------- shows ----------

// Show is a tracked show with everything the matcher needs.
type Show struct {
	ID             int64
	CanonicalName  string
	MaxEpisode     int
	AniListID      *int
	Source         string
	CadenceWeekday *int
	CadenceSource  string
	CadenceFetched *time.Time
	CreatedAt      time.Time
	Aliases        []string
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
	row := s.db.QueryRow(`SELECT id, canonical_name, max_episode, anilist_id, source,
		cadence_weekday, cadence_source, cadence_fetched_at, created_at FROM show WHERE id = ?`, id)

	var sh Show
	var weekday, anilistID sql.NullInt64
	var src, source, fetched, created sql.NullString
	if err := row.Scan(&sh.ID, &sh.CanonicalName, &sh.MaxEpisode, &anilistID, &source,
		&weekday, &src, &fetched, &created); err != nil {
		return nil, err
	}
	if weekday.Valid {
		w := int(weekday.Int64)
		sh.CadenceWeekday = &w
	}
	if anilistID.Valid {
		a := int(anilistID.Int64)
		sh.AniListID = &a
	}
	sh.Source = source.String
	sh.CadenceSource = src.String
	sh.CadenceFetched = parseTime(fetched)
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
func (s *Store) AddAlias(showID int64, alias string) error {
	if alias == "" {
		return nil
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO alias (show_id, name) VALUES (?, ?)`, showID, alias)
	return err
}

// SetAniListID records the AniList media id, making a re-import idempotent.
func (s *Store) SetAniListID(showID int64, anilistID int) error {
	_, err := s.db.Exec(`UPDATE show SET anilist_id = ? WHERE id = ?`, anilistID, showID)
	return err
}

// SetSource records where a show came from: anilist | schedule | manual.
func (s *Store) SetSource(showID int64, source string) error {
	_, err := s.db.Exec(`UPDATE show SET source = ? WHERE id = ?`, source, showID)
	return err
}

// SetMaxEpisode updates the plausibility bound used by the matcher.
func (s *Store) SetMaxEpisode(showID int64, max int) error {
	_, err := s.db.Exec(`UPDATE show SET max_episode = ? WHERE id = ?`, max, showID)
	return err
}

// GetShowByAniListID finds a previously imported show. Returns (nil, nil) when
// there is none, which is the normal case on a first import.
func (s *Store) GetShowByAniListID(anilistID int) (*Show, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM show WHERE anilist_id = ?`, anilistID).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.GetShow(id)
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

// SetCadence records the expected weekday and when we learned it. Used only for
// the "on a break, or is my client broken?" diagnostic — never load-bearing.
func (s *Store) SetCadence(showID int64, weekday int, source string, fetched time.Time) error {
	_, err := s.db.Exec(`UPDATE show SET cadence_weekday = ?, cadence_source = ?,
		cadence_fetched_at = ? WHERE id = ?`, weekday, source, fetched.UTC().Format("2006-01-02 15:04:05"), showID)
	return err
}

// SetNextEpisode records the schedule's authoritative next-episode point:
// episode n airs at t. This is the one fact animeschedule.net gives us, and it
// is held until a download confirms the episode is real.
func (s *Store) SetNextEpisode(showID int64, n int, t time.Time) error {
	_, err := s.db.Exec(`UPDATE show SET next_ep = ?, next_airs_at = ?,
		schedule_fetched_at = datetime('now') WHERE id = ?`,
		n, t.UTC().Format("2006-01-02 15:04:05"), showID)
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
func (s *Store) DeleteShow(id int64) error {
	_, err := s.db.Exec(`DELETE FROM show WHERE id = ?`, id)
	return err
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

// ---------- filters and preferences ----------

// Filter is a hard accept/reject predicate.
type Filter struct {
	Kind   string // group | resolution | size | source | codec
	Op     string // in | min | max | exclude
	Value  string
	Reason string // why the rule exists
}

// Preference is a soft ranking signal. Lower rank is better.
type Preference struct {
	Kind   string // group | codec | uncensored | source
	Value  string
	Rank   int
	Reason string // why this preference exists
}

func (s *Store) AddFilter(showID int64, f Filter) error {
	_, err := s.db.Exec(`INSERT INTO filter (show_id, kind, op, value, reason) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (show_id, kind, op, value) DO UPDATE SET reason = excluded.reason`,
		showID, f.Kind, f.Op, f.Value, nullIfEmpty(f.Reason))
	return err
}

// nullIfEmpty keeps an absent reason NULL rather than storing "", so "no reason
// given" stays distinguishable from "reason is empty".
func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// DeleteFilter removes a rule, so a later grade can retract an earlier one.
func (s *Store) DeleteFilter(showID int64, f Filter) error {
	_, err := s.db.Exec(
		`DELETE FROM filter WHERE show_id = ? AND kind = ? AND op = ? AND value = ?`,
		showID, f.Kind, f.Op, f.Value)
	return err
}

func (s *Store) Filters(showID int64) ([]Filter, error) {
	rows, err := s.db.Query(`SELECT kind, op, value, COALESCE(reason,'') FROM filter WHERE show_id = ? ORDER BY id`, showID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Filter
	for rows.Next() {
		var f Filter
		if err := rows.Scan(&f.Kind, &f.Op, &f.Value, &f.Reason); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// AddPreference records a soft ranking. Re-grading the same value updates the
// rank in place rather than adding a second, conflicting row.
func (s *Store) AddPreference(showID int64, p Preference) error {
	_, err := s.db.Exec(`INSERT INTO preference (show_id, kind, value, rank, reason) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (show_id, kind, value) DO UPDATE SET rank = excluded.rank, reason = excluded.reason`,
		showID, p.Kind, p.Value, p.Rank, nullIfEmpty(p.Reason))
	return err
}

func (s *Store) Preferences(showID int64) ([]Preference, error) {
	rows, err := s.db.Query(`SELECT kind, value, rank, COALESCE(reason,'') FROM preference WHERE show_id = ? ORDER BY rank, id`, showID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Preference
	for rows.Next() {
		var p Preference
		if err := rows.Scan(&p.Kind, &p.Value, &p.Rank, &p.Reason); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---------- rejected ----------

// AddRejected records a negative training signal with the reason, which
// determines whether the matcher or only the filters get updated.
func (s *Store) AddRejected(showID int64, releaseTitle, reason string) error {
	_, err := s.db.Exec(`INSERT INTO rejected (show_id, release_title, reason) VALUES (?, ?, ?)`,
		showID, releaseTitle, reason)
	return err
}

// ---------- seen infohashes ----------

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
