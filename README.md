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
| **Declare** | Shows live in `shows.yaml`: name, aliases, how many you've watched, season length |
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

> **The image may require authentication.** GitHub Packages are private by
> default, so if the pull fails with `denied` or `unauthorized`, either
> authenticate first (`docker login ghcr.io`) or build from source below.
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

## Design

[`DESIGN.md`](DESIGN.md) is the original design sketch, kept for context. It
predates the implementation and describes intent rather than current behaviour —
read it as history, not documentation.

## License

[MIT](LICENSE)
