<div align="center">

<img src="img/kishizu_logo_512.png" width="110" alt="kishizu logo">

# kishizu

A seasonal anime downloader. You name the shows you are watching this cour, and
episodes appear on disk as they air, then are deleted once you have watched them.

[![Release](https://img.shields.io/github/v/release/Ebonhawk3829/kishizu?style=flat-square)](https://github.com/Ebonhawk3829/kishizu/releases)
[![Docker Image](https://img.shields.io/badge/ghcr.io-ebonhawk3829%2Fkishizu-blue?style=flat-square&logo=docker)](https://github.com/Ebonhawk3829/kishizu/pkgs/container/kishizu)
[![License](https://img.shields.io/badge/license-MIT-green?style=flat-square)](LICENSE)
[![CI](https://img.shields.io/github/actions/workflow/status/Ebonhawk3829/kishizu/ci.yml?label=CI&style=flat-square)](https://github.com/Ebonhawk3829/kishizu/actions)

</div>

---

This is a personal tool published for reference. It is built around one setup,
and several assumptions are fixed rather than configurable. Read it, fork it, or
ignore it. See [Status](#status) before deploying it anywhere.

## What it does

kishizu watches Nyaa for the shows you have declared, works out which release is
the episode you are waiting for, hands it to Transmission, files it in your
library, and deletes it once you have watched it.

Air times come from animeschedule.net. If that site goes down, kishizu keeps
working with the air times it already has.

| Stage | What happens |
|---|---|
| **Declare** | Shows live in `shows.yaml`, or are added from the web UI by pasting an animeschedule.net URL |
| **Hunt** | Polls Nyaa RSS per show, parses each release, matches it to a show and episode |
| **Grab** | Hands the best matching release to Transmission, ranked by the global group order |
| **File** | Renames to `<Show> - E<NN>.mkv` in your library, ready for your player |
| **Watch** | An mpv script tells kishizu when you finish an episode |
| **Clean** | Deletes watched episodes, keeping the last few |

## Status

kishizu handles currently-airing seasons for one person. It assumes a specific
stack and a specific filesystem layout:

- **Currently-airing seasons only.** Back-catalogue and batch downloads are out
  of scope.
- **One episode at a time.** No season packs, no bulk backfill.
- **A specific stack.** Transmission for downloads, Syncthing to reach the
  desktop, mpv for playback, ntfy for notifications.
- **A specific filesystem layout.** Library paths and naming are fixed, because
  Syncthing and mpv are configured to match them.
- **Single user.** No auth, no multi-tenancy, no permissions model.

The core parts (release parsing, per-group episode offsets, the episode cycle)
are not tied to any of that, and could be split out later. That is not planned.

If you need something general-purpose today, use
[autobrr](https://github.com/autobrr/autobrr) or
[Sonarr](https://sonarr.tv/) with an anime indexer.

## Installation

You need a running [Transmission](https://transmissionbt.com/) instance with RPC
enabled. Everything else is optional.

### Docker Compose

```yaml
services:
  kishizu:
    image: ghcr.io/ebonhawk3829/kishizu:0.7.1
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

Pin a version rather than using `:latest`. This tool is not on a compatibility
promise.

kishizu is dry-run by default: it polls, matches and logs what it would
download, but hands nothing to Transmission until you remove `-dry-run`.

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
| `watched` | How many episodes you have already seen |
| `max` | Season length; `0` if unknown |

Re-run with `-seed` after editing. Adding shows from the web UI also works.

### Adding a show by URL

Paste the show's animeschedule.net URL:

```
https://animeschedule.net/anime/re-zero-kara-hajimeru-isekai-seikatsu-4
```

The trailing part is the show's **slug**, an exact identity on the schedule.
kishizu stores it and reads the show's own page by it. No title matching is
involved.

From the page it also fills in:

- the **season length**, which is otherwise typed by hand and usually left at 0
- every **alternative name** (romaji, English, synonyms) as an alias
- the **release time**, a full timestamp with a UTC offset, which is episode 1's
  broadcast slot. It is the raw airing, the earliest native broadcast, so the
  hunt may open a few hours before a subbed upload appears
- the **next episode** and when it airs, from the page's countdown. The daily
  refresh reads this; a page with no countdown means the season has finished
- the **cover art**, from the page's canonical `og:image`

Japanese names and abbreviations are not imported. Nyaa release titles are
romanised, so a Japanese name can never appear in one, and a short one can clear
the alias threshold against an unrelated show on token overlap alone.

Shows added before slugs existed can be backfilled with `-backfill-slugs`, which
reads a hand-maintained `name: slug` map (see `slugs.yaml.example` for the
shape). Resolving a name to the right season needs a judgement call, so it is
done once by hand.

### Flags

| Flag | Default | Purpose |
|---|---|---|
| `-db` | `kishizu.db` | SQLite database path |
| `-config` | `shows.yaml` | Show seed file used by `-seed` |
| `-serve` | — | Address for the web UI, e.g. `:8098` |
| `-transmission` | `http://<tailnet-ip>:9091/transmission/rpc` | Transmission RPC endpoint |
| `-library` | `/media/anime` | Library root for finished episodes, as kishizu sees it |
| `-staging` | `/downloads/anime` | Staging root Transmission downloads into, as kishizu sees it |
| `-keep` | `2` | Recently watched episodes to keep on disk |
| `-interval` | `5m` | RSS poll interval |
| `-dry-run` | `true` | Decide but do not download |
| `-infer` | — | Derive group offsets from the feed instead of training by hand |
| `-backfill-slugs` | — | Attach animeschedule slugs from the mapping file and enrich from the schedule |
| `-slugs` | `slugs.yaml` | Name to slug mapping used by `-backfill-slugs` |
| `-prefer` | `VARYG,Erai-Raws,SubsPlease,ToonsHub` | Preferred release groups, best first |
| `-ntfy` | `http://<tailnet-ip>:8085/kishizu` | ntfy topic for notifications; empty disables |
| `-debug` | `false` | Verbose logging of every decision |
| `-show` | — | Only run this show (substring match on canonical name) |
| `-list` | — | List tracked shows with next episode and air dates |
| `-train` | — | Train a show (substring match on canonical name) |
| `-ep` | `0` | Episode number to train against (0 = next unwatched) |

## How matching works

Release groups disagree about episode numbering. For one cour of a long-running
show, one group posts `E01` while another posts `E41`, and both are the same
episode. kishizu stores an **offset per release group**, so `[VARYG] Show - 47`
and `[Erai-raws] Show - 07` can both resolve to episode 7.

Offsets are learned either by training (confirm the parse of a few releases, one
per numbering convention) or by `-infer`, which derives them from the structure
of each group's numbering. Both are reviewable and resettable per show in the UI.

Training calibrates the parser. It does not set preferences. Quality policy
(resolution floor, codec ranking, batch rejection, dub demotion, group order) is
global and set in advance, and is never written by training. A training run
teaches two things: the per-group episode offset, and the vocabulary, meaning
that a token in a title maps to a canonical value so every future release using
that spelling is readable.

A show is not hunted until it has been trained. Without at least one known group
offset there is nothing to reason with, so polling would only burn requests.
Untrained shows show as *needs training* in the UI.

Training also needs a release to train on, so a show whose first episode is
still in the future shows as *upcoming*. Some shows are announced without a
scheduled slot; those stay *upcoming* until the site publishes a time.

### Aliases are a gate, not a score

An alias decides whether a release is eligible for a show. It does not contribute
to how good a match looks.

The alias set is wide, because the schedule page contributes romaji, English,
Japanese and synonyms, and those names carry different amounts of identity.
Scoring them made the short ones dangerous: an abbreviation like `ReZero 4` is a
perfect match against any release containing those two tokens, so it inflated
confidence for releases that merely looked similar.

Any alias that clears the threshold makes the release a candidate, and how well
it cleared is discarded. Choosing which candidate to download is left to the
criteria that distinguish releases (group, resolution, codec, source), which the
global rules and the group order already rank.

Abbreviations are still stored, but tagged and excluded from matching. They are
used as Nyaa feed queries, where a broad net is what you want.

## Watch signal

An mpv Lua script (`scripts/mpv/kishizu-watch.lua`) posts the finished file to
kishizu once playback nears the end. It only reports files under a configured
root, so using mpv for other media will not spam the server.

Failed posts are spooled and retried on the next mpv start, and also raise an
ntfy alert. A missed signal leaves a file on disk, which is the safe direction.

## FAQ

<details>
<summary><strong>Is this ready for general use?</strong></summary>

No. See [Status](#status). It is published as a reference and a starting point
for forking.

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

No. Currently-airing seasons only.

</details>

<details>
<summary><strong>Why is my new show not downloading anything?</strong></summary>

It probably has not been trained. A show needs at least one release group's
episode offset before kishizu can tell which release is the episode you are
waiting for, so untrained shows are not polled at all. They show as
*needs training* in the UI.

Train it from the show's row: open Train and confirm the parse of a release for
the episode you are waiting for. One example per numbering convention is enough.

</details>

<details>
<summary><strong>Do I have to use animeschedule.net URLs?</strong></summary>

In practice, yes. Adding a show means pasting its animeschedule.net URL, which
is how kishizu knows which show you mean. The slug in the URL is an exact
identity, and the show's own page carries the season length, every alternative
name, the cover art and the next episode's air time.

A plain name still works, but such a show has no slug, so it never gets an air
date from the schedule. It will sit until you train it and it picks up a
release.

</details>

## Design

[`DESIGN.md`](DESIGN.md) is the original design sketch, kept for context. It
predates the implementation and describes intent rather than current behaviour.

## License

[MIT](LICENSE)
