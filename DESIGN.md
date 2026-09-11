# kishizu — design notes

> **Status: historical.** This document was written *before* kishizu existed, as
> a design sketch and research log. It is kept because the reasoning is still
> interesting — the measurements, the rejected alternatives, and the two
> findings that shaped the implementation (per-group offsets, and hard filters
> vs soft preferences).
>
> **It is not documentation.** Where it disagrees with the code, the code wins.
> Several sections describe things that were never built, or were built
> differently. For what kishizu actually does, see [README.md](README.md).

Written 2026-09-07. Implementation followed over the following days.

## Layers, in the order they act

Straight-line order: what happens first, second, third. Each layer is independently
testable and depends only on the ones above it.

| # | Layer | What it does | How (planned) |
|---|---|---|---|
| **1** | **Store** | Holds shows, aliases, offsets, filters, preferences, episode state | SQLite (like `dig`). Tables: `show`, `group_offset`, `filter`, `preference`, `episode`, `rejected`. Hard filters and soft preferences are **separate tables** — load-bearing |
| **2** | **List builder** | Produces the season's watch list | One scrape of animeschedule.net (`<h2 class="show-title-bar">` → `Ep N` → `<time datetime>`), ~137 shows. Click to add. Their title becomes an **alias**, never canonical. Cadence stored for diagnostics only |
| **3** | **Release parser** | Turns a release title into structured fields | Regex: `[Group]` prefix, `SxxEyy`, bare `- NN`, season words, resolution, codec, source. Port of `testdata/match.py` |
| **4** | **Matcher** | Decides *which show* and *which episode* a release is | Normalised-token recall over aliases (threshold 0.6). Then `episode = raw − offset`, where offset is **per release group** with a show default. Unseen groups try the show's known offset set, keeping `1 ≤ ep ≤ max_episode` |
| **5** | **Trainer** | Learns aliases + offsets from examples | Propose-and-confirm loop. Search Nyaa HTML, rank by **uncertainty** (not confidence), propose 3, user answers **with a reason**. `offset = raw − true_ep` per group. One example per *numbering convention* suffices |
| **6** | **Listener** | Notices new uploads | Poll `nyaa.si/?page=rss&c=1_2&q=<alias>` per show, ~5 min. Dedupe on `infohash`. RSS covers 14–33 days, so backfill is free |
| **7** | **Filter** | Accept or reject a candidate | Hard predicates only: group ∈ set, resolution ≥ 1080p (**floor, not ladder**), 500MB–2GB, not batch, not dub |
| **8** | **Ranker** | Orders accepted candidates | Soft preferences: uncensored first, then group order, then codec (x264 > x265 > AV1). Applied after the delay window |
| **9** | **Delay window** | Waits for a better release | Hold pending set ~10–15 min, then rank and pick. Backlog binging is out of scope so latency is free |
| **10** | **Downloader** | Gets the bytes, stops seeding | Transmission RPC (`transmission-remote -a <magnet>`). A done-script removes the torrent on completion, leaving files on disk |
| **11** | **Library placement** | Puts the file where Syncthing sees it | `<library>/<Show>/<Show> - E<NN>.mkv`, Windows-safe sanitisation at creation |
| **12** | **Watch signal** | Learns you finished an episode | Bespoke mpv script → tool. Removes AniList from the loop entirely |
| **13** | **Deletion** | Frees space | On `watched`, delete file, mark `deleted`, keep N most recent watched |
| **14** | **UI** | Add shows, train, see status, intervene | Single page + pop-outs for per-show config. Go + `html/template`, roughly `dig`'s shape |

### Dependency direction

```
1 Store ─────────────────────────────────────────────┐
   │                                                 │
2 List builder → 3 Parser → 4 Matcher → 5 Trainer    │
                                                     │
6 Listener → 7 Filter → 8 Ranker → 9 Delay           │
                                                     │
10 Downloader → 11 Placement → 12 Watch → 13 Delete  │
                                                     │
14 UI ───────────────────────────────────────────────┘
```

Layers 3–5 are **prototyped and passing** (11/11 reference set, 0 false positives on 75 live
uploads). Layers 1–2 and 6–14 are designed but not built.

## Why this exists

AniList's API returned `403 "The AniList API has been temporarily disabled due to severe
stability issues"` for 5+ days continuously (as of 2026-09-07). The tool it replaced
depended on AniList for its watch list, episode schedule, and watch progress — so the
entire pipeline stalled: no new downloads **and** no deletion of watched episodes.

The conclusion is not "harden it against outages." It's that a personal, single-user,
single-season tool should not have a runtime dependency on a third-party tracker at all.

## Scope

**In scope**
- Currently-airing seasons and cours, episode by episode
- Automatic download when a matching release appears
- Deletion after watching

**Out of scope (deliberately)**
- Batch / pack downloads of older series — handled manually
- Binging a backlog — different process, different tool
- Multi-user, multi-account, custom lists, blacklists
- Series/offset machinery for chaining cours (PREQUEL walking, absolute series axis)
- Any runtime dependency on AniList

## The workflow (how it is used)

1. **I come prepared with a season to watch.**
2. **Build the list** by browsing animeschedule.net and clicking the shows. The site's
   naming differs from what I'd type (`Clevatess II: Majuu no Ou...` vs `Clevatess S2`) —
   that's fine here, because I'm recognising titles in a list, not matching strings.
   Airing dates come along as a bonus.
3. **Train each show once.** For the first aired episode, I point at the Nyaa torrents I
   would have been happy with, ranked. The tool infers the filters from the examples.
4. **Tool builds its own filters** from those examples — release groups, resolution,
   source/codec, size range, and the episode-number offset.
5. **RSS listener** polls Nyaa. When something clears the threshold, grab it — with a
   short delay window so a later, better release can win.
6. **Torrent downloads**, Syncthing moves it to the PC.
7. **Watch in mpv.** A small bespoke script pings the tool that I'm finished → episode deleted.

---

# Part 2 — How each step works

The workflow above is agreed. This section is the mechanism: what each step actually does,
what it stores, and what it depends on. Written to be implemented top-to-bottom.

## Data model

Everything lives in one local store (SQLite, like `dig`). No external source of truth.

```
show
  id
  canonical_name        -- mine, used for folder names
  aliases[]             -- every other title we've seen (romaji, alt, schedule's name)
  max_episode           -- plausibility bound for offset inference (nullable)
  cadence_weekday       -- optional, from animeschedule, diagnostic only
  cadence_source        -- where cadence came from + when fetched
  created_at

group_offset            -- per (show, release group)
  show_id
  group                 -- "SubsPlease", "Erai-raws", "(none)"
  offset                -- raw_number - true_episode
  learned_from          -- which training example established it

filter                  -- hard accept/reject, per show
  show_id
  kind                  -- group | resolution | size | source | codec
  op                    -- in | min | max | exclude
  value

preference              -- soft ranking, per show, ordered
  show_id
  kind                  -- group | codec | uncensored | source
  value
  rank                  -- lower = better

episode
  show_id
  number                -- LOCAL episode number
  state                 -- wanted | downloading | downloaded | watched | deleted
  infohash
  release_title
  file_path
  downloaded_at
  watched_at

rejected                -- negative training signal
  show_id
  release_title
  reason                -- wrong_episode | wrong_show | batch | dub | codec | quality | other
```

**Hard filters and preferences are separate tables.** This is load-bearing — see the
training-UX finding below.

## Step 2 — Build the list

**Mechanism:** one scrape of `https://animeschedule.net`, parsed with a single regex:

```
<h2 class="show-title-bar...">TITLE</h2>
  … <span class="show-episode">Ep N</span>
  … <time datetime="2026-09-08T01:00+10:00">
```

Yields ~137 shows with next-episode number and absolute air timestamp. Verified working.

**Flow:** render the scraped list → user clicks the ones they want → for each, create a
`show` with `canonical_name` = the user's own name (typed or edited) and the schedule's
title stored as an **alias**. The schedule's name is never canonical — it's a poor key for
matching release names.

**Cadence** (`weekday` + interval) is stored if available, marked with its fetch date, and
used **only** for the "on break or broken?" diagnostic. Never load-bearing.

**Failure mode:** scrape fails → user adds shows by hand. Everything else still works.

## Step 3/4 — Training (propose-and-confirm loop)

**Seed:** user provides one release they'd accept, and states which episode it is.

**Then the loop:**

1. Tool searches Nyaa HTML (`?f=0&c=1_2&q=<alias>&s=seeders&o=desc`) — **not RSS**, because
   RSS can't paginate and training needs the full result set.
2. For each candidate, compute a confidence score:
   - title match < 0.6 → discard
   - episode readable and == target → confident
   - episode readable and != target → discard
   - **matches show but episode unreadable → score 0.5, maximum uncertainty**
3. Rank by **uncertainty** (closest to 0.5 first), not confidence. This is what surfaces
   the cases that need a human.
4. Present top 3. User answers with a **reason**, not just yes/no.
5. Refit: accepted examples update `group_offset` and `aliases`; rejections update
   `filter`/`preference` **only if the reason says to**.
6. Repeat until user says finished.

**Why the reason matters** (found in simulation): for Tomb Raider King the tool proposed
three releases that were all the *correct episode* — the user would reject them for quality
(H.265, 2160p, wrong source). A binary "no" would teach the matcher that ToonsHub ep 9 is a
bad match, corrupting it. So:

| Answer | Effect |
|---|---|
| Yes / good | accept; update offsets + aliases |
| Yes / acceptable | accept; update offsets; record lower preference rank |
| No — wrong episode | update matcher (this is a matching failure) |
| No — wrong show | add negative alias signal |
| No — batch | add `filter(kind=batch, exclude)`; **do not** touch matcher |
| No — dub | add `filter(kind=source, exclude=dub)` |
| No — codec (e.g. HEVC) | add `preference(kind=codec)` — ranking only |
| No — quality/size | add `filter` or `preference` per kind |

**Offset inference:** `offset = raw_number_in_title − true_episode`, grouped by release
group. Unseen groups are tried against the set of offsets the show has exhibited, keeping
results where `1 <= ep <= max_episode`. Verified: 1 example per *numbering convention* is
sufficient, not 1 per group.

**Batches are excluded from proposals** — out of scope by definition, and they parse as
"no episode number" which makes them look maximally uncertain.

## Step 5 — Listener

**Mechanism:** poll **one RSS feed per show**, every ~5 minutes:

```
https://nyaa.si/?page=rss&c=1_2&q=<url-encoded alias>
```

- RSS accepts `c` (category) and `q` (search) — verified. It ignores `p` (pagination) and
  `s`/`o` (sort), neither of which we need here.
- 75 items per feed is plenty: measured 14–33 days of coverage per show.
- Each item carries `pubDate`, `nyaa:infoHash`, `nyaa:seeders`, `nyaa:size`, `nyaa:trusted`,
  `nyaa:remake`, `nyaa:categoryId`.

**Why per-show rather than one global feed:** a global feed is truncated at 75 items
(~6 hours) and would miss anything during a longer gap. Per-show feeds give weeks of
coverage, so **backfill after downtime is free** — just read the feed, no HTML needed.

**Cost:** one request per show per poll. With ~10 shows at 5-minute intervals that's
~2,880 requests/day. Worth confirming against Nyaa's tolerance; if too heavy, poll the
global feed normally and only fall back to per-show feeds on startup/backfill.

**Per item:**
1. Skip if `infohash` already seen (dedupe without downloading)
2. Match against the show → episode
3. Apply hard `filter` rows → reject or keep
4. If kept, add to a pending set for that (show, episode)

**Delay window:** hold the pending set for ~10–15 min, then pick the best by `preference`
order and hand off. Lets a later better release win. Backlog binging is out of scope, so
the latency costs nothing.

**Resolution is a floor, not a ladder.** `>= 1080p` means 1080p and above are acceptable;
below is rejected. No fallback to lower resolutions — if nothing at or above the floor appears,
the episode is simply not downloaded. (A resolution *ladder* was considered and rejected: it
adds complexity the user did not ask for, and `>=` already covers the "higher is fine" case.)

**Time-window guard:** if cadence is known, reject items whose `pubDate` predates the
episode's air date. Lower bound only — never a tight upper bound, because v2 re-uploads and
remakes land late and are *good* candidates.

**Backfill:** free. On startup, read each show's RSS feed — it covers 14–33 days, so any
gap shorter than that is caught automatically. No HTML search required. (This corrects an
earlier version of this document which claimed backfill needed HTML.)

## Step 6 — Download

**Mechanism:** Transmission RPC, on the local network. Runs as a non-root user
and mounts the media tree.

- Add: `transmission-remote -a <magnet>`
- On completion, a Transmission done-script removes the torrent:
  `sleep 5; transmission-remote -t "$TR_TORRENT_HASH" -r`
- Result: file on disk, torrent removed, not seeding → Syncthing picks it up

**No BitTorrent client to write.** The tool is a coordinator.

**Folder structure:** `<library>/<Show Name>/<Show Name> - E<NN>.mkv`, with Windows-safe
sanitisation applied at creation (see below).

## Step 7 — Watch signal and deletion

**Mechanism:** bespoke mpv script POSTs to the tool when an episode finishes.

- Removes AniList from the loop entirely — previously mpv → AniList → downloader, a round trip
  between two machines the user owns, via a third party.
- Routing: tailnet HTTP POST (immediate) or a file drop Syncthing already syncs (zero new
  network surface). Not yet decided.

**Deletion:** on `watched`, delete the file and mark `episode.state = deleted`. Keep N most
recent watched episodes (configurable, currently 2) so a mis-mark isn't fatal.

## Cross-cutting: duplicate guards

Three distinct things get called "a duplicate." They need different guards, and conflating
them is how you either re-download forever or miss a genuinely better release.

| Kind | Example | Guard |
|---|---|---|
| **Same release seen twice** | Identical torrent reappears on the next poll | `infohash` already seen → skip |
| **Same episode, different release** | SubsPlease 47 and Erai-raws 07 both = ep 7 | Episode already `wanted`/`downloading`/`downloaded` → skip. **Unless** the new one ranks higher *and* is within the delay window |
| **Episode already consumed** | Watched and deleted last week; a late remake appears | **Terminal state → skip.** See below |
| **Better version of a release** | `v2`, `REPACK`, `PROPER`, remake of the same group's upload | Not a duplicate — but only an upgrade while the episode is still live |

### The consumed-episode hole (and why it matters)

The obvious rule — "skip if the episode is already downloaded" — **fails exactly in the case
that motivated this tool**. Once an episode is watched and deleted, its state is `deleted`,
not `downloaded`. A late or re-release would match "not currently downloaded" and be
re-grabbed: the file comes back, Syncthing pushes it to the PC again, and it sits there
until someone notices.

This is not hypothetical. Nyaa has a `remake` field precisely because re-uploads happen, and
the RSS window is 14–33 days — long enough that an episode can be watched and deleted well
inside it.

**Fix: episode state is a one-way latch, and `watched`/`deleted` are terminal.**

```
wanted → downloading → downloaded → watched → deleted
                                       ↑         ↑
                                       └─────────┘  terminal: never re-enter
```

Once an episode reaches `watched` or `deleted`, **no release for that episode is ever
auto-grabbed again**, regardless of quality, group, or how much better it ranks. The user
finished with it; bringing it back is not an upgrade, it's a resurrection.

**Rules:**

1. **Infohash is the identity of a release.** Dedupe on it unconditionally. It's in the RSS
   (`nyaa:infoHash`), so this works without downloading.
2. **Episode state is the identity of an episode, and it only moves forward.** Reaching
   `watched` or `deleted` is terminal.
3. **The delay window is the upgrade window.** While an episode is still *pending* (inside the
   10–15 min window), a better-ranked candidate replaces the worse one. Once the window closes
   and the download starts, the episode is locked.
4. **`nyaa:remake` and `v2`/`REPACK`/`PROPER` are upgrades only while the episode is live.**
   If it's already `downloaded`, surface for manual action (auto-replacing risks clobbering
   something mid-watch). If it's `watched` or `deleted`, ignore entirely.
5. **Blocked episodes stay blocked.** A manually blocked `(show, episode)` is never
   reconsidered, regardless of ranking.
6. **Re-grabbing is always available manually.** The latch governs *automatic* behaviour
   only. If you genuinely want an episode back, one click un-latches it.

**Failure direction:** if the guard is uncertain, **skip**. Re-downloading wastes bandwidth
and disk and resurrects files the user already finished with; missing an upgrade costs
nothing. This matches the general principle that failure should never destroy data — and here
it also means never *resurrect* it.

## Cross-cutting: filename safety

Syncthing moves files between Linux and Windows. At creation time, sanitise both folder and
file names:

- Strip `< > : " / \ | ? *` and control characters
- Avoid reserved device names: `CON PRN AUX NUL COM1-9 LPT1-9`
- No trailing dots or spaces

## Failure philosophy

Every external dependency degrades rather than blocks:

| Dependency | If it fails |
|---|---|
| animeschedule.net | Add shows by hand; lose cadence diagnostic only |
| Nyaa RSS | No new downloads; nothing is deleted or corrupted |
| Nyaa HTML search | No training, no backfill; listener unaffected |
| Transmission | Downloads queue; state preserved |
| mpv script | No watch signal; nothing auto-deleted (safe direction) |

The last one matters: **failure should never delete.** A missed watch signal leaves files
on disk; a false one destroys them.

---

## Key measurements (verified 2026-09-07)

**Nyaa RSS volume** — `https://nyaa.si/?page=rss`
- 75 items spanning 5.7 hours ≈ **13 uploads/hour** unfiltered
- Filtered to English-translated (`1_2`): 32 in that window ≈ **5–6/hour**
- RSS exposes: `title`, `pubDate`, `nyaa:seeders`, `nyaa:leechers`, `nyaa:infoHash`,
  `nyaa:size`, `nyaa:trusted`, `nyaa:remake`, `nyaa:categoryId`

**CORRECTION (2026-09-08): 75 is PER PAGE, not the total.**
The `1_2` (English-translated) category is ~100+ pages deep at 75/page — roughly
**7,500–8,600 entries**, not 75. Verified: `p=100` returns 75 rows, `p=120` returns 0.

**RSS does NOT paginate.** `?page=rss&p=2` returns a set 75/75 identical to a freshly
fetched `p=1` — the `p` parameter is ignored on RSS (same limitation as Nyaa's RSS
ignoring `s=seeders&o=desc`).

**But this does not matter, because RSS accepts filters.** Verified 2026-09-08:

| Query | Works? |
|---|---|
| `?page=rss&c=1_2` | Yes — returns only English-translated |
| `?page=rss&c=1_2&q=Tomb+Raider+King` | Yes — per-show feed |
| `?page=rss&u=SubsPlease` | Yes — per-release-group feed |

So instead of one global feed truncated at 75, we can poll **one feed per show**, and 75
items is far more than enough. Measured coverage of a per-show feed:

| Show | Items | Window covered |
|---|---|---|
| Tomb Raider King | 75 | **33 days** |
| BLEACH Sennen Kessen | 75 | **14 days** |

**Consequence: RSS alone is sufficient for the listener AND for backfill.** A 33-day
window means the tool could be down for weeks and still catch up from RSS. The earlier
conclusion that backfill needs HTML was wrong — it assumed a single unfiltered feed.

**Where HTML is still needed:** training (step 3). The propose-and-confirm loop wants a
broad candidate pool including *wrong* releases to learn from, and benefits from
`s=seeders&o=desc` ordering. A per-show RSS feed is already filtered to plausible matches,
which is the opposite of what training wants. RSS also ignores sort order, so training
can't rank by seeders.

Revised split:
- **Listener + backfill → RSS** (per show, `c=1_2&q=<alias>`)
- **Training → HTML search** (broad pool, sortable, paginated)

**Token frequency in 75 live uploads** (grounds the filter defaults)
`WEB` 25 · `WEB-DL` 15 · `BD` 14 · `HEVC` 13 · `Multi-Sub` 13 · `AAC` 11 · `AV1` 9 ·
`x265` 8 · `10bit` 8 · `Opus` 7 · `WEBRip` 6 · `H.264` 5 · `Remux` 3 · `x264` 1

**animeschedule.net** — server-rendered, no API key, no JS
- Pattern: `<h2 class="show-title-bar">TITLE</h2>` … `<span class="show-episode">Ep N</span>`
  … `<time datetime="2026-09-08T01:00+10:00">`
- One fetch yielded 137 unique shows with next-episode number + absolute air timestamp
- Covers a ~1 week window (2026-09-07 → 09-13 in the sample)

**Sources tested and rejected**
| Source | Result |
|---|---|
| AniList | `403` — still down |
| Jikan (MAL) | `504` — MAL down too |
| `api.animeschedule.net/v1` | connection refused |
| Crunchyroll | `403` Cloudflare challenge |
| LiveChart | `200` but client-rendered, no timestamps in HTML |
| anime-offline-database | 41,537 entries but 95% FINISHED, 580 ONGOING, 164 with AniList IDs; current season only as `UPCOMING`. Stale for airing shows. |

## Design decisions

**No global defaults.** Every show is configured explicitly when added. Adding a show to a
new season is expected to involve a small amount of setup, and that is accepted — no
inheritance, no assumptions. (User preference, stated explicitly.)

**Training over configuration.** Step 3 is labelling, not form-filling. Point at ranked
examples; the tool proposes inferred rules; confirm. The tool should *propose* rather than
silently infer, so the rules stay inspectable.

**The schedule is a convenience, not an authority.** animeschedule.net is used to build the
initial list and to carry airing dates for diagnostics ("is this a mid-season break or is my
client broken?"). It is not the source of truth for what exists — Tomb Raider King was absent
from the schedule page but present on Nyaa as `S01E09`.

**Canonical name is mine, not theirs.** The schedule's romaji titles are poor keys for
matching against release names. Store their title as an *alias*; keep my own name canonical.

**Time-window filter is asymmetric.** Reject uploads whose `pubDate` predates the air date
(a release cannot predate its own broadcast — safe, kills false positives from old torrents
and batch leftovers). Do **not** impose a tight upper bound: v2 re-uploads, remakes, and
groups catching up land late and are *good* candidates. Nyaa has a `remake` field.

**Delay window before grabbing.** Short (10–15 min) so a later better release can win.
Backlog binging is out of scope, so the latency cost doesn't apply.

## Filter shape (user's own words)

> "anything from these release groups that is at least 1080p, and uncompressed (whatever
> codecs that implies); 500mb - 2gb, rank uncensored first if available, then only by
> release group"

That is five predicates and one ordering — a fraction of the previous tool's `Priorities` struct
(`criteria_order`, `fansubs`, `resolutions`, `sources`, `codecs`, `audio`, `ignore_list`,
plus `min_seeders`, two size ceilings, adaptive pagination).

**"Uncompressed" resolved as a preference, not a filter.** User's clarification: *"prefer
x264 over x265"* — i.e. codec is a **ranking signal, not an accept/reject predicate**. This
sidesteps the ambiguity entirely: a preference orders candidates without excluding any, so
the scarcity of x264 (6 of 75 live uploads) no longer matters, and AV1 (9 of 75, rising)
isn't wrongly excluded.

Implication for the model: **hard filters** (accept/reject) and **soft preferences**
(ranking) are separate lists. Conflating them is what made the original phrasing ambiguous.

| Kind | Examples |
|---|---|
| Hard filter | group ∈ set, resolution ≥ 1080p, 500MB ≤ size ≤ 2GB |
| Soft preference | x264 > x265 > AV1, uncensored first, group order |

## Pieces required

| # | Piece | Notes |
|---|---|---|
| 1 | Season list | Built by browsing animeschedule; stored locally |
| 2 | Show identity + aliases | My canonical name + their title + any others |
| 3 | Episode offset | Absolute → local numbering; inferred from training |
| 4 | New-upload feed | Nyaa RSS poll, ~5 min |
| 5 | Match: upload → show | **The hard part.** Title variants + offset |
| 6 | Episode number | Parsed from release name |
| 7 | Filter / rank | Per-show, trained |
| 8 | Downloader | **Transmission** (already running; a done-script stops seeding) |
| 9 | Library placement | Where the file lands |
| 10 | Watch signal | Bespoke mpv script → tool |
| 11 | Deletion policy | Delete watched, keep N recent |
| 12 | Persistent state | Downloaded / deleted / blocked |
| 13 | UI | Add shows, train, see status, intervene |

## Borrowed from Seanime (verified)

`isSeasonAndEpisodeMatch` is a good small design and uses **no air dates**:
1. Parse release name → title, episode number(s), season, group, resolution
2. Reject if >1 episode number parsed (likely a batch)
3. If episode > current count, subtract absolute offset → if in range, it's absolute-numbered
4. Reject if still out of range
5. Reject if already watched

Steps 2 and 4 are the duplicate/wrong-episode guards. Step 3 needs one integer per show.

## Downloader

**Transmission — already running, already does exactly what's needed.**

The audiobooks pipeline already implements "download then stop seeding so files can be
copied," via a Transmission done-script:

```sh
#!/bin/sh
# Fires on torrent completion (Transmission sets TR_TORRENT_* env vars).
# Removes the torrent from the list; downloaded files stay on disk.
sleep 5   # let the daemon finish final bookkeeping before the RPC call
/usr/bin/transmission-remote -t "$TR_TORRENT_HASH" -r
```

Transmission exposes an RPC endpoint; adding a torrent is one call with a magnet
or URL. It runs as a non-root user and mounts the media tree.

**Decision: reuse Transmission.** No BitTorrent client to write or maintain. The tool
becomes a coordinator: poll RSS → match → rank → hand magnet to Transmission → completion
script fires → file is on disk and no longer seeding → Syncthing picks it up.

User's constraint: *"whatever, so long as it downloads then stops seeding so we can copy it
via syncthing — even something more lightweight and headless is totally fine if the code is
robust."* Transmission satisfies this and is already proven in this stack.

## UI

**Single page with pop-outs for per-season config.** (User's stated preference.)

Implications:
- Step 3 (point at ranked torrents) needs a screen — a CLI would make labelling painful
- The season list, per-show filters, and current status all live on one page
- Per-show config opens as a pop-out/modal rather than a separate route
- Roughly the shape of `dig`'s UI: one main view + modals, Go + `html/template`

## Test-bed results (2026-09-08)

A throwaway matcher (`testdata/match.py`) was built and iterated against 11 hand-labelled
reference torrents supplied by the user, then run against 75 live Nyaa uploads.

**Result: 11/11 on the reference set, 0 false positives on the live feed.**

### Finding 1: the offset is per-GROUP, not per-show

This is the single most important discovery. Within **one** show, groups disagree about
numbering. BLEACH TYBW episode 7:

| Group | Release says | Convention |
|---|---|---|
| Erai-raws | `- 07` | local |
| SubsPlease | `- 47` | absolute |
| ToonsHub | `S01E47` | absolute |
| VARYG | `S01E47` | absolute |

A single show-level offset cannot work: 40 is right for three groups and wrong for the
fourth (7 − 40 = −33). **Offset must be keyed by release group, with a show-level default.**

This matches a `packAxis` finding from the tool kishizu replaced — three numbering hypotheses —
but applies it to *episode* releases, not just packs.

### Finding 2: title matching is easier than feared

Simple normalised-token **recall** (fraction of alias tokens present in the release title)
separated cleanly:

- All 11 reference torrents scored **1.00**
- Highest-scoring non-match on the live feed: **0.33** (`Skeleton Knight in Another World`
  vs `Re:ZERO ... Another World` — shares "another world")
- Threshold 0.6 sits in a wide empty gap

Caveat: 11 samples is small, and the aliases were hand-written from the reference titles.
Real risk is shows with generic names, not the matching algorithm.

### Finding 3: alias quality is what matters, not algorithm

`Tomb Raider King` matched via its alternate title `Dogul Wang` — the user supplied this.
`BLEACH` needed the romaji `Sennen Kessen Hen`. The matcher is dumb; the aliases do the
work. This supports the training UX: **the tool's job is to make alias entry easy**, not to
be clever.

### Finding 4: RSS ignores pagination but accepts filters

`?page=rss&p=2` returns a set 75/75 identical to a fresh `p=1` — **`p` is ignored on RSS**.
But `c` (category) and `q` (search) both work, so we poll **one feed per show** and 75 items
covers 14–33 days. Backfill is therefore free — see the correction under Key measurements.
(This heading previously said "RSS is a new-uploads feed only" and claimed backfill needed
HTML; that was wrong and is corrected above.)

## Training UX: propose-and-confirm (prototyped 2026-09-08)

User's idea, which is better than "user hunts for examples":

> me: this is a good example
> tool: got it, so these?
> me: no for #2, yes for the others
> tool: ok, like these
> me: yes, finished

The tool searches, proposes candidates it is *least certain about*, and the user answers
yes/no. Each answer refits the model. Loop until the user says finished.

### Prototype result: 11/11, converges in 1–2 rounds

Simulated offline (`testdata/active.py`) against 319 real Nyaa search results.

BLEACH, seeded with one Erai-raws example, proposed in round 1:
```
[YES] BLEACH Thousand Year Blood War S01E47 THE END 2 ...   (no plausible offset)
[no ] BLEACH Thousand Year Blood War S01E46 THE END ...     (no plausible offset)
[YES] [SubsPlease] Bleach - Sennen Kessen Hen - 47 ...      (no plausible offset)
```
Two yes answers → offsets `[0, 40]` learned → **5/5 on the full accepted set**, including
two groups never proposed.

**The "no plausible offset" reason is the signal that drives the loop.** It means "this
matches your show but I can't read the episode number" — precisely the case that needs a
human. Ranking by *uncertainty* (not confidence) is what surfaces it first.

### Design flaw found: "no" is ambiguous

The simulation shows why a binary yes/no is not enough. Tomb Raider King round 1:
```
[no] [AnoZu]     Tomb Raider King S01E09 1080p CR WEB-DL AAC 2.0 H.264
[no] [ToonsHub]  Tomb Raider King S01E09 1080p BILI WEB-DL AAC2.0 H.265
[no] [Feibanyama] Tomb Raider King S01E09 [IQIYI WebRip 2160p HEVC AAC Multi-Su
```
All three are **the correct episode**. The user rejected them for *quality* — H.265, 2160p,
wrong source — not because they were wrong releases. A binary "no" would teach the tool
that episode 9 from ToonsHub is bad, which is wrong and would break future matching.

**Fix: the answer must carry a reason.** Minimum viable set:
- **Yes** — good, use it
- **No, wrong episode/show** — a matching failure; update the model
- **No, wrong quality** — a *preference* signal; do not touch the matcher

This falls straight out of the hard-filter / soft-preference split already recorded above.
The training loop must not conflate them.

### Also observed

- Re:ZERO round 1 proposed episodes 81/80/78 — the model had only seen offset 0, so
  absolute-numbered releases were implausible. Correct behaviour, but it means **the seed
  example should ideally come from a group using the dominant convention**, or the tool
  should ask about numbering explicitly up front.
- Batch releases (`(41-44)`, `393-406`) parse as "no episode number" and get proposed.
  They should be filtered out of training proposals entirely — out of scope by definition.

## Prior art reviewed (2026-09-08)

### autobrr — right architecture, wrong domain

Model is **filter → action**: filters match releases, actions dispatch them (qBittorrent,
Deluge, **Transmission**, watch folder, exec script, webhook). Supports plain RSS, regex
filtering, Go + SQLite.

That is very close to the hard-filter → preference → action shape we arrived at
independently — good confirmation the shape is right.

**Why not integrate it:** its filters are regex on the release title. Our core problem needs
*arithmetic* — `S01E47` → episode 7 via a per-group offset. Regex cannot subtract 40; you'd
need one rule per episode per group. Its main advantage (IRC announces for racing the initial
swarm) is moot for Nyaa, which has no IRC announce. And RSS polling is ~50 lines at our
measured volume (5–6 relevant uploads/hour). Adding a service would mean syncing state across
two systems for no gain.

**Verdict: borrow the architecture (already have it), not the tool.**

### Seanime — patterns worth stealing

From `internal/torrents/autoselect/search.go`:

1. **Resolution fallback ladder** — try each resolution in preference order until one returns
   results. **Rejected:** the user's spec is `>= 1080p`, a floor, not a preference. A ladder
   would add complexity for a case the floor already handles (higher resolutions pass).
2. **Batch/single fallback with a quality gate** — try batch; if it fails *or* returns weak
   results, retry as single. Gate: `maxSeeders >= 15 || nbFound > 2`. Useful as a "did this
   search actually succeed?" check, especially in the training loop. **Partially adopted** —
   the gate idea is kept for the training loop; batch handling is out of scope entirely.
3. **Dedupe by infohash across concurrent sources** — already planned; confirmed correct.

### Apprise

Notification library. Not relevant — included in error.

## Adopted from prior art

**Search-quality gate.** Don't propose training candidates from a weak result set. (From
Seanime's `validateBatchResults`.)

**Infohash dedupe** across sources. (Already planned; confirmed by Seanime.)

**Explicitly rejected: resolution ladder.** Resolution stays a hard floor (`>= 1080p`).

## Not yet decided (at the time of writing)

These were open questions when this was written. Most have since been settled by
just building it; they're left as a record of what was uncertain.

- ~~Whether to integrate an existing RSS listener tool or own the feed polling~~
  → **Own it.** autobrr can't express episode arithmetic; polling is trivial at our volume.
- Whether animeschedule.net coverage is good enough to be worth the scrape long-term
- Whether the delay window is per-show or global
- Exact pop-out contents for per-show config
- How the mpv "finished" signal is routed (tailnet HTTP POST vs file drop)
- Store: SQLite (like `dig`) vs plain JSON files
- Whether `max_episode` is entered by the user or inferred from cadence

**Resolved since:** SQLite; the mpv signal is an HTTP POST; `max_episode` is
entered by the user; the schedule scrape is used and refreshed daily.

## Build order (as proposed)

Start from the top, one layer at a time, each independently testable:

1. **Store + show CRUD** — add a show, aliases, nothing else
2. **Parser + matcher** — port `testdata/match.py`; test against the reference set
3. **Trainer** — port `testdata/active.py`; propose-and-confirm with reasons
4. **Listener** — RSS poll → match → filter → pending set
5. **Download handoff** — Transmission RPC + completion handling
6. **Watch signal + deletion** — mpv script and the delete path
7. **UI** — single page + pop-outs, once the above is proven

Layers 2–3 were prototyped in Python and ported. This is roughly the order the
implementation followed.

## Name

Settled as **kishizu** (季雫) — short, and following the precedent set by `dig`:
one word, tied to the domain. The candidates above were discarded.
