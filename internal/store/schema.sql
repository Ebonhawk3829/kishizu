-- kishizu schema.
--
-- Design notes that are load-bearing:
--   * filter and preference are SEPARATE tables. Hard accept/reject and soft
--     ranking are different concepts; conflating them is what made the original
--     "uncompressed" spec ambiguous.
--   * group_offset is per (show, group), not per show. Groups disagree about
--     episode numbering within a single show.
--   * episode.state is a one-way latch; watched/deleted are terminal.

CREATE TABLE IF NOT EXISTS show (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    canonical_name TEXT    NOT NULL UNIQUE,
    max_episode    INTEGER NOT NULL DEFAULT 0,   -- 0 = unknown, no upper bound
    -- anilist_id makes a re-import idempotent: the import is a bootstrap and
    -- AniList is flaky, so it may need more than one run to complete.
    anilist_id     INTEGER,
    source         TEXT    NOT NULL DEFAULT 'manual',  -- anilist | schedule | manual
    cadence_weekday INTEGER,                     -- 0-6, NULL when unknown
    cadence_source TEXT,                         -- where cadence came from
    cadence_fetched_at TEXT,                     -- when, for staleness
    created_at     TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_show_anilist ON show(anilist_id) WHERE anilist_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS alias (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    show_id INTEGER NOT NULL REFERENCES show(id) ON DELETE CASCADE,
    name    TEXT    NOT NULL,
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

-- Hard accept/reject predicates.
CREATE TABLE IF NOT EXISTS filter (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    show_id INTEGER NOT NULL REFERENCES show(id) ON DELETE CASCADE,
    kind    TEXT    NOT NULL,   -- group | resolution | size | source | codec
    op      TEXT    NOT NULL,   -- in | min | max | exclude
    value   TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_filter_show ON filter(show_id);

-- Soft ranking. Lower rank sorts better.
CREATE TABLE IF NOT EXISTS preference (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    show_id INTEGER NOT NULL REFERENCES show(id) ON DELETE CASCADE,
    kind    TEXT    NOT NULL,   -- group | codec | uncensored | source
    value   TEXT    NOT NULL,
    rank    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_preference_show ON preference(show_id);

CREATE TABLE IF NOT EXISTS episode (
    show_id       INTEGER NOT NULL REFERENCES show(id) ON DELETE CASCADE,
    number        INTEGER NOT NULL,   -- LOCAL episode number
    state         TEXT    NOT NULL DEFAULT 'wanted',
    infohash      TEXT,
    release_title TEXT,
    file_path     TEXT,
    downloaded_at TEXT,
    watched_at    TEXT,
    PRIMARY KEY (show_id, number)
);
CREATE INDEX IF NOT EXISTS idx_episode_state ON episode(state);

-- Negative training signal. reason determines whether the matcher or only the
-- filters/preferences get updated.
CREATE TABLE IF NOT EXISTS rejected (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    show_id       INTEGER NOT NULL REFERENCES show(id) ON DELETE CASCADE,
    release_title TEXT    NOT NULL,
    reason        TEXT    NOT NULL,
    created_at    TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_rejected_show ON rejected(show_id);

-- Every infohash we have ever acted on, so a release that reappears after its
-- episode row is gone is still recognised as seen.
CREATE TABLE IF NOT EXISTS seen (
    infohash  TEXT PRIMARY KEY,
    show_id   INTEGER,
    episode   INTEGER,
    seen_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
