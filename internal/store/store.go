// Package store persists shows, aliases, offsets, filters, preferences and
// episode state in SQLite.
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"time"

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
		return fmt.Errorf("apply schema: %w", err)
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
	Kind  string // group | resolution | size | source | codec
	Op    string // in | min | max | exclude
	Value string
}

// Preference is a soft ranking signal. Lower rank is better.
type Preference struct {
	Kind  string // group | codec | uncensored | source
	Value string
	Rank  int
}

func (s *Store) AddFilter(showID int64, f Filter) error {
	_, err := s.db.Exec(`INSERT INTO filter (show_id, kind, op, value) VALUES (?, ?, ?, ?)`,
		showID, f.Kind, f.Op, f.Value)
	return err
}

func (s *Store) Filters(showID int64) ([]Filter, error) {
	rows, err := s.db.Query(`SELECT kind, op, value FROM filter WHERE show_id = ? ORDER BY id`, showID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Filter
	for rows.Next() {
		var f Filter
		if err := rows.Scan(&f.Kind, &f.Op, &f.Value); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) AddPreference(showID int64, p Preference) error {
	_, err := s.db.Exec(`INSERT INTO preference (show_id, kind, value, rank) VALUES (?, ?, ?, ?)`,
		showID, p.Kind, p.Value, p.Rank)
	return err
}

func (s *Store) Preferences(showID int64) ([]Preference, error) {
	rows, err := s.db.Query(`SELECT kind, value, rank FROM preference WHERE show_id = ? ORDER BY rank, id`, showID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Preference
	for rows.Next() {
		var p Preference
		if err := rows.Scan(&p.Kind, &p.Value, &p.Rank); err != nil {
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
