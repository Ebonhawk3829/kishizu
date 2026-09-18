-- kishizu schema.
--
-- Design notes that are load-bearing:
--   * group_offset is per (show, group), not per show. Groups disagree about
--     episode numbering within a single show.
--   * episode.state is a one-way latch; watched/deleted are terminal.
--
-- Release quality policy (resolution floor, codec rank, batch reject, dub
-- demotion, group order) is GLOBAL and hardcoded in internal/release/rules.go.
-- It is set in advance and never written by training; training calibrates the
-- parser only (per-group offsets and the vocabulary table below).

-- Schema versioning. Each migration step is recorded here; see migrate() in
-- store.go for how this is used.
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER NOT NULL
);
INSERT OR IGNORE INTO schema_version (version) VALUES (0);

CREATE TABLE IF NOT EXISTS show (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    canonical_name TEXT    NOT NULL UNIQUE,
    max_episode    INTEGER NOT NULL DEFAULT 0,   -- 0 = unknown, no upper bound
    source         TEXT    NOT NULL DEFAULT 'manual',  -- schedule | manual
    cadence_weekday INTEGER,                     -- 0-6, NULL when unknown
    cadence_source TEXT,                         -- where cadence came from
    cadence_fetched_at TEXT,                     -- when, for staleness
    -- The schedule's authoritative next-episode point, from animeschedule.net.
    -- Held until a download confirms that episode is real: when ep N is
    -- grabbed, next_ep becomes N+1 and next_airs_at is projected forward a
    -- week. NULL when the show is not on the schedule.
    next_ep         INTEGER,
    next_airs_at    TEXT,
    schedule_fetched_at TEXT,
    -- The animeschedule.net slug, e.g. "re-zero-kara-hajimeru-isekai-seikatsu-4".
    -- An exact identity for the show on the schedule. When present the daily
    -- refresh matches on this instead of fuzzy-matching titles, which is a
    -- guess that fails whenever a show is on break, outside the ~1 week
    -- timetable window, or titled differently in romaji vs English.
    -- NULL when the show has no schedule page (films, unlisted shows).
    slug            TEXT,
    created_at     TEXT    NOT NULL DEFAULT (datetime('now'))
);
-- Two shows must never claim the same schedule page. Partial: films and
-- unlisted shows have no slug and are simply not indexed.
CREATE UNIQUE INDEX IF NOT EXISTS idx_show_slug ON show(slug) WHERE slug IS NOT NULL;

-- Vocabulary: the words release groups actually use, mapped to the canonical
-- values kishizu reasons about.
--
-- The parser knows a fixed word list, so a release written in an unexpected
-- vocabulary reads as nothing and cannot be ranked. Training supplies both
-- halves — the canonical value and the token that meant it — and the pair is
-- recorded here. One correction makes every future release using that token
-- readable, for every group and every show.
CREATE TABLE IF NOT EXISTS vocabulary (
    kind      TEXT NOT NULL, -- resolution | codec | source | service | audio
    token     TEXT NOT NULL, -- as written in the title, e.g. "AVC"
    canonical TEXT NOT NULL, -- what kishizu calls it, e.g. "h.264"
    PRIMARY KEY (kind, token)
);

CREATE TABLE IF NOT EXISTS alias (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    show_id INTEGER NOT NULL REFERENCES show(id) ON DELETE CASCADE,
    name    TEXT    NOT NULL,
    -- Where this alias came from. Provenance matters because aliases arrive
    -- from three places and they are not equivalent:
    --   manual   - typed by the user
    --   schedule - the animeschedule show page (romaji/english/japanese/synonyms)
    --   training - extracted from a release title during a training run
    -- Abbreviations from the schedule are stored as source 'abbreviation':
    -- too short to match on safely, but useful as a Nyaa feed query.
    source  TEXT    NOT NULL DEFAULT 'manual',
    UNIQUE (show_id, name)
);
CREATE INDEX IF NOT EXISTS idx_alias_show ON alias(show_id);

-- Per-group episode offset. "(none)" is the key used when a release has no
-- bracketed group prefix.
CREATE TABLE IF NOT EXISTS group_offset (
    show_id      INTEGER NOT NULL REFERENCES show(id) ON DELETE CASCADE,
    group_name   TEXT    NOT NULL,
    offset_value INTEGER NOT NULL,
    learned_from TEXT,
    PRIMARY KEY (show_id, group_name)
);

CREATE TABLE IF NOT EXISTS episode (
    show_id       INTEGER NOT NULL REFERENCES show(id) ON DELETE CASCADE,
    number        INTEGER NOT NULL,   -- LOCAL episode number
    state         TEXT    NOT NULL DEFAULT 'wanted',
    infohash      TEXT,
    release_title TEXT,
    file_path     TEXT,
    -- When this episode is expected to air, projected from the schedule's
    -- next-episode point and the cadence weekday. NULL when unknown. This is
    -- what distinguishes "hasn't aired yet" from "should have aired".
    airs_at       TEXT,
    downloaded_at TEXT,
    watched_at    TEXT,
    PRIMARY KEY (show_id, number)
);
CREATE INDEX IF NOT EXISTS idx_episode_state ON episode(state);

-- Every infohash we have ever acted on, so a release that reappears after its
-- episode row is gone is still recognised as seen.
CREATE TABLE IF NOT EXISTS seen (
    infohash  TEXT PRIMARY KEY,
    show_id   INTEGER,
    episode   INTEGER,
    seen_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
