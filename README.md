<div align="center">

<img src="img/kishizu_logo_512.png" width="110" alt="kishizu logo">

# kishizu

**Declare the season. Watch the episodes. Stop thinking about it.**

A seasonal anime downloader: you name the shows you're watching this cour, and
episodes appear on disk as they air — then disappear once you've watched them.

[![Release](https://img.shields.io/github/v/release/Ebonhawk3829/kishizu?style=flat-square)](https://github.com/Ebonhawk3829/kishizu/releases)
[![Docker Image](https://img.shields.io/badge/ghcr.io-ebonhawk3829%2Fkishizu-blue?style=flat-square&logo=docker)](https://github.com/Ebonhawk3829/kishizu/pkgs/container/kishizu)
[![License](https://img.shields.io/badge/license-MIT-green?style=flat-square)](LICENSE)
[![CI](https://img.shields.io/github/actions/workflow/status/Ebonhawk3829/kishizu/ci.yml?label=CI&style=flat-square)](https://github.com/Ebonhawk3829/kishizu/actions)

</div>

---

> **Please read this before using kishizu.**
>
> This is a **personal tool, published for reference — not a distribution.**
> It is built around one person's very specific setup, and several of its
> assumptions are baked in rather than configurable. It works well for that
> setup and will likely fight you on a different one.
>
> You are welcome to read it, learn from it, or fork it into something that
> suits your own tastes. That is why it's public. But please don't expect to
> drop it into your stack and have it work.
>
> See [Status and intent](#status-and-intent) for the honest version.

## What it does

kishizu watches Nyaa for the shows you've declared, works out which release is
the episode you're actually waiting for, hands it to Transmission, files it in
your library, and deletes it once you've watched it.

The part that matters: **there is no runtime dependency on a third-party
tracker.** AniList is a one-time bootstrap and nothing more. Air times come from
a weekly schedule scrape. If either service goes down, kishizu keeps working —
which is the entire reason it exists.

| Stage | What happens |
|---|---|
| **Declare** | Shows live in `shows.yaml`, or are added from the web UI by pasting an animeschedule.net URL |
| **Hunt** | Polls Nyaa RSS per show, parses each release, matches it to a show and episode |
| **Grab** | Hands the best matching release to Transmission, per your group preferences |
| **File** | Renames to `<Show> - E<NN>.mkv` in your library, ready for your player |
| **Watch** | An mpv script tells kishizu when you finish an episode |
| **Clean** | Deletes watched episodes, keeping the last few |

## Status and intent

kishizu is **not** a general-purpose anime manager, and isn't trying to be one
yet. It was built to replace a tool that broke completely when AniList's API
went down for five days, and every design decision follows from that one goal:
*no runtime dependency on anything I don't control.*

That goal produces constraints which are reasonable for me and arbitrary for
everyone else:

- **Currently-airing seasons only.** Back-catalogue and batch downloads are
  deliberately out of scope — you handle those yourself.
- **One episode at a time.** No season packs, no bulk backfill.
- **A specific stack.** Transmission for downloads, Syncthing to reach the
  desktop, mpv for playback, ntfy for notifications. These are assumed, not
  pluggable.
- **A specific filesystem layout.** Library paths and naming are fixed, because
  Syncthing and mpv are configured to match them.
- **Single user.** No auth, no multi-tenancy, no permissions model.

None of these are limitations of the idea — they're the shape of one person's
setup, and they're why the tool is small enough to actually work.

**A more generic build may come later.** The core — release parsing, per-group
episode offsets, the propose-and-confirm trainer, the four-state episode cycle —
is genuinely reusable and not tied to any of the above. Splitting that into a
configurable core with pluggable downloaders, libraries and watch signals is a
plausible future direction. It isn't planned, and it isn't promised.

If that's what you need today, you'd be better served by
[autobrr](https://github.com/autobrr/autobrr) or
[Sonarr](https://sonarr.tv/) with an anime indexer.

## Installation

> **Before you start:** you need a running
> [Transmission](https://transmissionbt.com/) instance with RPC enabled.
> Everything else is optional.

### Docker Compose

```yaml
services:
  kishizu:
    image: ghcr.io/ebonhawk3829/kishizu:0.1.0
    user: "1000:1000"             # match your media user
    environment:
      - TZ=Pacific/Auckland
    volumes:
      - ./configs/kishizu:/data   # database + shows.yaml
      - ./media/anime:/media/anime
    ports:
      - "8098:8098"
    restart: unless-stopped
    command:
      - "-db"
      - "/data/kishizu.db"
      - "-config"
      - "/data/shows.yaml"
      - "-serve"
      - "0.0.0.0:8098"
      - "-dry-run"                # remove to actually download
```

Then:

```sh
docker compose up -d
```

Open **http://localhost:8098**.

> Pinning a version rather than using `:latest` is recommended — this tool
> is not on a compatibility promise.

> **Dry-run by default.** kishizu polls, matches and logs what it *would*
> download, but hands nothing to Transmission until you remove `-dry-run`.
> Leave it on until you trust the matching.

### From source

```sh
git clone https://github.com/Ebonhawk3829/kishizu.git
cd kishizu
go build -o kishizu ./cmd/kishizu
./kishizu -db kishizu.db -config shows.yaml -seed
./kishizu -db kishizu.db -serve :8098 -dry-run
```

## Configuration

Shows are declared in `shows.yaml`:

```yaml
shows:
  - name: BLEACH: Thousand-Year Blood War - The Calamity
    aliases:
      - Bleach: Sennen Kessen Hen - Kashin Tan
      - Bleach S17
    watched: 7
    max: 10
```

| Field | Meaning |
|---|---|
| `name` | Canonical name, as animeschedule.net lists it |
| `aliases` | Other spellings to match against Nyaa releases |
| `watched` | How many episodes you've already seen |
| `max` | Season length; `0` if unknown |

Re-run with `-seed` after editing. Adding shows from the web UI also works.

### Adding a show by URL

The best way to add a show is to paste its animeschedule.net URL:

```
https://animeschedule.net/anime/re-zero-kara-hajimeru-isekai-seikatsu-4
```

The trailing part is the show's **slug** — an exact identity on the schedule.
kishizu stores it and looks the show up by it. There is no title matching
anywhere in the schedule lookup: either the show is on the timetable this week,
or it isn't.

From the page it also fills in:

- the **season length**, which is otherwise typed by hand and usually left at 0
- every **alternative name** (romaji, English, synonyms) as an alias
- the **release time**, a full timestamp with a UTC offset, which is episode 1's
  broadcast slot. For a show that hasn't premiered it is the only air
  information that exists, since the timetable only covers about a week. It is
  the *raw* airing — the earliest native broadcast — so the hunt may open a few
  hours before a subbed upload appears, which is harmless
- the **cover art**, from the page's canonical `og:image`

Japanese names and abbreviations are deliberately not imported. Nyaa release
titles are romanised, so a Japanese name can never appear in one — and a short
one can clear the alias threshold against an unrelated show on token overlap
alone, which silently points a show at another show's air times.

Shows added before slugs existed can be backfilled with `-backfill-slugs`,
which reads a hand-maintained `name: slug` map (see `slugs.yaml` for the
shape). Resolving a name to the right season needs a judgement call, so it is
done once by hand rather than guessed every day.

### Flags

| Flag | Default | Purpose |
|---|---|---|
| `-db` | `kishizu.db` | SQLite database path |
| `-config` | `shows.yaml` | Show seed file |
| `-serve` | — | Address for the web UI, e.g. `:8098` |
| `-transmission` | `http://100.64.0.1:9091/transmission/rpc` | Transmission RPC endpoint |
| `-library` | `/downloads/anime` | Library root, as Transmission sees it |
| `-keep` | `2` | Recently watched episodes to keep on disk |
| `-interval` | `5m` | RSS poll interval |
| `-dry-run` | `true` | Decide but don't download |
| `-infer` | — | Derive group offsets from the feed instead of training by hand |
| `-backfill-slugs` | — | Attach animeschedule slugs from the mapping file and enrich from the schedule |
| `-slugs` | `slugs.yaml` | Name → slug mapping used by `-backfill-slugs` |
| `-prefer` | `VARYG,Erai-Raws,SubsPlease,ToonsHub` | Preferred release groups, best first |
| `-ntfy` | — | ntfy topic for notifications; empty disables |

## How matching works

Release groups disagree about episode numbering. For one cour of a long-running
show, one group posts `E01` while another posts `E41` — both are the same
episode. kishizu stores an **offset per release group**, so `[VARYG] Show - 47`
and `[Erai-raws] Show - 07` can both resolve to episode 7.

Offsets are learned either by training (propose-and-confirm, a few examples per
numbering convention) or by `-infer`, which derives them from the structure of
each group's numbering. Both are reviewable and resettable per show in the UI.

**A show is not hunted until it has been trained.** Without at least one known
group offset there is nothing to reason with, so polling would only burn
requests to conclude what was already known. Untrained shows show as
*needs training* in the UI rather than a misleading *up to date*.

### Aliases are a gate, not a score

An alias decides whether a release is **eligible** for a show. It does not
contribute to how good a match looks.

That distinction matters because the alias set is wide — the schedule page alone
contributes romaji, English, Japanese and synonyms, and those names carry very
different amounts of identity. Scoring them made the short ones dangerous: an
abbreviation like `ReZero 4` is a perfect match against any release containing
those two tokens, so it inflated confidence for releases that merely looked
similar.

So: any alias that clears the threshold makes the release a candidate, and how
well it cleared is discarded. Choosing *which* candidate to download is left to
the criteria that genuinely distinguish releases — group, resolution, codec,
source — which is what the preference ranker already does.

Abbreviations are still stored, but tagged and excluded from matching. They are
used as Nyaa feed queries, where a broad net is what you want.

## Watch signal

An mpv Lua script (`scripts/mpv/kishizu-watch.lua`) posts the finished file to
kishizu once playback nears the end. It only reports files under a configured
root, so using mpv for other media won't spam the server.

Failed posts are spooled and retried on the next mpv start, and also raise an
ntfy alert — a missed signal leaves a file on disk, which is the safe direction.

## FAQ

<details>
<summary><strong>Is this ready for general use?</strong></summary>

No. See [Status and intent](#status-and-intent). It's published as a reference
and a starting point for forking, not as something to deploy.

</details>

<details>
<summary><strong>Why not just use Sonarr?</strong></summary>

If Sonarr fits your setup, use it. kishizu exists because the author wanted no
runtime dependency on a metadata provider, and wanted per-group episode offsets
handled as a first-class problem. Those are narrow goals.

</details>

<details>
<summary><strong>Does it seed?</strong></summary>

No. Torrents are removed on completion. This is a personal tool on a small
server; seeding was never a goal.

</details>

<details>
<summary><strong>Does it handle batch or back-catalogue downloads?</strong></summary>

No, deliberately. Currently-airing seasons only.

</details>

<details>
<summary><strong>What happens if AniList goes down?</strong></summary>

Nothing, because kishizu doesn't ask it anything at runtime. That's the point.

</details>

<details>
<summary><strong>Why is my new show not downloading anything?</strong></summary>

It probably hasn't been trained. A show needs at least one release group's
episode offset before kishizu can tell which release is the episode you're
waiting for, so untrained shows are not polled at all. They show as
*needs training* in the UI.

Train it from the show's row: pick the episode, and confirm which of the
proposed releases is that episode. One example per numbering convention is
enough.

</details>

<details>
<summary><strong>Do I have to use animeschedule.net URLs?</strong></summary>

Yes, in practice. Adding a show means pasting its animeschedule.net URL — that
is how kishizu knows which show you mean. The slug in the URL is an exact
identity, so there is no title matching anywhere in the schedule lookup: either
the show is on the timetable this week, or it isn't.

A plain name still works, but such a show has no slug, so it never gets an air
date from the schedule. It will sit until you train it and it picks up a
release.

</details>

## Design

[`DESIGN.md`](DESIGN.md) is the original design sketch, kept for context. It
predates the implementation and describes intent rather than current behaviour —
read it as history, not documentation.

## License

[MIT](LICENSE)
