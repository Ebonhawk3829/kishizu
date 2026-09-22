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

kishizu watches an indexer for the shows you have declared, works out which
release is the episode you are waiting for, hands it to your torrent client,
files it in your library, and deletes it once you have watched it.

Air times come from animeschedule.net. If that site goes down, kishizu keeps
working with the air times it already has.

Two background jobs keep that current, each writing to its own store so the UI
only ever reads: air times for your tracked shows, daily; the seasonal browse
list, weekly.

The weekly job is the only thing that talks to animeschedule about shows you
are not tracking. It pulls the season, drops shows that have finished, and fills
in each entry's English title, season length and art. Adding a show then copies
from that cache rather than fetching the page, so it is instant and works when
the site is down. A show not on the current season falls back to its own page.

A page that 404s is not treated as finished on the strength of one sighting —
pages vanish transiently during a site update. Three consecutive daily misses
is the threshold, and a page that comes back clears the count.

| Stage | What happens |
|---|---|
| **Declare** | Add shows by browsing the season, pasting an animeschedule.net URL, or listing them in the config file |
| **Hunt** | Polls the indexer per show, parses each release, matches it to a show and episode |
| **Grab** | Hands the best matching release to your torrent client, ranked by your group order |
| **File** | Renames it into your library, ready for your player |
| **Watch** | Anything that can POST JSON tells kishizu when you finish an episode |
| **Clean** | Deletes watched episodes, keeping the last few |

## Contents

- [Status](#status)
- [Requirements](#requirements)
- [Installation](#installation)
- [Configuration](#configuration)
- [Adding shows](#adding-shows)
- [How matching works](#how-matching-works)
- [Episode states](#episode-states)
- [Adopting a finished season](#adopting-a-finished-season)
- [Watch signal](#watch-signal)
- [Flags](#flags)
- [API](#api)
- [Security](#security)
- [License](#license)

## Status

kishizu handles currently-airing seasons for one person.

Releases follow semver. Patch releases are safe to take unread. Minor
releases may add configuration keys, and existing ones keep working. A major
release may change behaviour, and the release notes will say so.

What it does not do:

- **Hunt a back catalogue.** It tracks shows week by week while they air. A
  finished season can be [adopted](#adopting-a-finished-season) instead.
- **Download batches or season packs** as part of the airing pipeline. One
  episode at a time.
- **Support multiple users.** No auth, no permissions model. See
  [Security](#security).

The core parts — release parsing, per-group episode offsets, the episode cycle
— are not tied to any of that, and could be split out later. That is not
planned.

If you need something general-purpose today, use
[autobrr](https://github.com/autobrr/autobrr) or
[Sonarr](https://sonarr.tv/) with an anime indexer.

## Requirements

- A torrent client with a remote API: **Transmission** or **qBittorrent**.
- Go 1.27+ if building from source. Docker needs nothing else.
- Optionally, a notification service: **ntfy** or **Gotify**.

Everything else is optional. kishizu runs dry by default, so you can point it
at your library and watch what it decides before it downloads anything.

## Installation

### Docker Compose

An example deployment, using Transmission as the client. Adjust the image tag,
the client settings and the paths to your setup — qBittorrent works the same
way (see the [flags](#flags) table).

```yaml
services:
  kishizu:
    image: ghcr.io/ebonhawk3829/kishizu:1.1.2   # or :latest
    user: "1000:1000"             # your media user's uid:gid
    volumes:
      - ./config:/data            # database, config file, caches
      - ./media:/media            # library and staging — one mount
    ports:
      - "127.0.0.1:8098:8098"
    restart: unless-stopped
    command:
      - "-db"
      - "/data/kishizu.db"
      - "-config"
      - "/data/kishizu.yaml"
      - "-serve"
      - "0.0.0.0:8098"
      - "-transmission"
      - "http://transmission:9091/transmission/rpc"
      - "-staging"
      - "/media/downloads/anime"
      - "-library"
      - "/media/anime"
      - "-dry-run=false"          # kishizu is dry-run by default; this line
                                  # is what switches downloading on
```

Then open **http://localhost:8098**.

Three things about that compose file:

**Mount `./media:/media` as one mount, not two.** The final move from staging
to library is a rename, and a rename cannot cross mount points. Separate
mounts for library and staging fail with `invalid cross-device link`.

**`-serve` must be `0.0.0.0:8098` inside the container.** kishizu binds to
localhost by default, which Docker's port mapping cannot reach. The host side
of the mapping (`127.0.0.1:8098:8098`) is what keeps it off your network.

**Dry-run is the default.** With `-dry-run=false` in the command above, kishizu
downloads for real. Drop that line to stay dry: it polls, matches and logs
what it would download, and hands nothing to your torrent client.

### From source

```sh
git clone https://github.com/Ebonhawk3829/kishizu.git
cd kishizu
go build -o kishizu ./cmd/kishizu
./kishizu -db kishizu.db -config kishizu.yaml -seed
./kishizu -db kishizu.db -serve 127.0.0.1:8098
```

## Configuration

One file holds everything: the server settings and the show list. Every server
key is optional — omit one and the default applies.

```yaml
server:
  library: /media/anime          # where finished episodes are filed
  staging: /downloads/anime      # where the client puts completed files
  keep: 2                        # recently watched episodes to leave on disk
  interval: 5m                   # how often to poll
  dry_run: true                  # decide but do not download

  downloader:
    kind: transmission           # transmission or qbittorrent
    transmission_rpc: http://transmission:9091/transmission/rpc

  notifier:
    kind: none                   # ntfy, gotify or none

  indexer:
    base: https://nyaa.si
    category: "1_2"              # anime-english-translated on Nyaa
    min_interval: 1s             # be polite to public indexers

  quality:
    resolution_floor: 1080p      # below this is rejected, not demoted
    group_order:                 # best first; unlisted groups still eligible
      - SubsPlease
      - Erai-Raws

  naming:
    preset: kishizu              # kishizu, sonarr, plex or custom

shows:
  - name: Example Show
    aliases:
      - Example Show Romaji Title
    watched: 0
    max: 12
```

See [`kishizu.yaml.example`](kishizu.yaml.example) for the annotated version.

**Configure this first if you are moving to kishizu mid-season** from another
tool and want to pre-seed the database: run with `-seed` once the settings are
right, so shows land with the paths and policy you intend.

### Settings in the UI

**Settings** in the web UI edits this file. Paths and the downloader are
captured at startup, so those need a restart; everything else takes effect on
save.

### Secrets

The torrent client password and the notification token are stored in this file
in plain text. To keep them out of it entirely, supply them by environment
variable instead:

| Variable | Replaces |
|---|---|
| `KISHIZU_QBITTORRENT_PASS` | `downloader.qbittorrent_pass` |
| `KISHIZU_GOTIFY_TOKEN` | `notifier.gotify_token` |

An environment variable wins when set and non-empty, and is never written back
into the file. The UI masks secrets and never displays them.

### Naming

| Preset | Layout |
|---|---|
| `kishizu` | `<library>/<Show>/<Show> - E09.mkv` |
| `sonarr` | `<library>/<Show>/Season 01/<Show> - S01E09.mkv` |
| `plex` | `<library>/<Show>/Season 01/<Show> - s01e09.mkv` |
| `custom` | your own pattern |

A custom pattern uses `{show}`, `{season}`, `{season:2}`, `{episode}`,
`{episode:2}`. It must contain `{show}` and an episode placeholder — without
the episode, kishizu cannot read the number back out of a filename, and the
watch signal would never match.

## Adding shows

Everything lives under **Add a show**.

**Browse the season.** Open **Browse this season** for the cached timetable —
every show on the current season, with the filter narrowing it. Pick one and
the slug is an exact identity, so the season length, cover art and every
alternative name are filled in for you. The list loads the first time you open
the panel, not on page load.

The list shows either the romaji or the English name: switch with the toggle in
the panel, or set `browse.title` in the config file. The filter matches both
either way, so switching never hides a show you could have found. Shows with no
separate English name fall back to the romaji one.

Browsing reads from a cache on disk and never touches the network, so the list
is there even when animeschedule is down. A background job refreshes it weekly
and fills in the English titles; a fresh container fetches once at startup, so
the panel is never empty. **Refresh** in the panel forces it now.


**Paste a URL.** An animeschedule.net URL (or a bare slug) does the same thing:

```
https://animeschedule.net/anime/re-zero-kara-hajimeru-isekai-seikatsu-4
```

A plain show name is rejected: the slug is the identity that fills in the
season length, cover art and air dates, and a name alone carries none of that.
To add a show by name, list it in the config file and run `-seed` — that is
also how you pre-seed a fresh database in one go. A show seeded without a slug
gets its air dates once you attach one with `-backfill-slugs`.

Japanese names and abbreviations are not imported. Release titles are
romanised, so a Japanese name can never appear in one, and a short one can
clear the alias threshold against an unrelated show on token overlap alone.

## How matching works

Release groups disagree about episode numbering. For one cour of a long-running
show, one group posts `E01` while another posts `E41`, and both are the same
episode. kishizu stores an **offset per release group**, so `[VARYG] Show - 47`
and `[Erai-raws] Show - 07` can both resolve to episode 7.

Offsets are learned either by training (confirm the parse of a few releases, one
per numbering convention) or by `-infer`, which derives them from the structure
of each group's numbering. Both are reviewable and resettable per show in the UI.

Training calibrates the parser: a training run teaches the per-group episode
offset and the vocabulary, meaning that a token in a title maps to a canonical
value so every future release using that spelling is readable. Quality policy
(resolution floor, codec ranking, batch rejection, dub demotion, group order)
sits outside training entirely — it is global, set in advance in the config
file, and applies to every show the same way.

A show starts hunting once it has been trained. Without at least one known group
offset there is nothing to reason with, so polling would only burn requests.
Untrained shows show as *needs training* in the UI.

Training also needs a release to train on, so a show whose first episode is
still in the future shows as *upcoming*. Some shows are announced without a
scheduled slot; those stay *upcoming* until the site publishes a time.

## Episode states

| State | Meaning |
|---|---|
| *upcoming* | The season has not started. Nothing to do. |
| *hunting* | Aired, within 72 hours, no release grabbed yet. Polled every 3 minutes. |
| *downloading* | Handed to the torrent client, not on disk yet. |
| *ready to watch* | On disk, waiting for you. |
| *missing* | Was on disk and is not now. Needs a decision: re-grab or mark watched. |
| *no release found* | The 72 hour window closed with nothing grabbed. |
| *up to date* | Watched or deleted. |

An episode in *downloading* sits outside polling: it cannot be re-grabbed, so
polling would evaluate releases nothing can act on. Quality is settled before
the grab instead, by the global rules and the group order.

## Adopting a finished season

The airing pipeline handles shows week by week. For a season that has already
finished, kishizu can adopt a release from [releases.moe](https://releases.moe)
(SeaDex), a community index of the highest-quality release for a given anime.

Open **Adopt a finished season** and paste the entry URL. kishizu reads the
release, lists every file in it, and proposes which are episodes. Each file gets
a checkbox and an episode dropdown, so you confirm or correct the proposals
before anything downloads.

The same thing is available from the command line:

```sh
./kishizu -db kishizu.db -adopt "https://releases.moe/112124/"
```

The URL's path is the AniList id, so no lookup is needed. The title and episode
numbers are derived from the filenames, and extras (NCOP, NCED, OVA, specials)
are left unchecked by default. `-adopt-episodes` overrides the proposal, one
number per file, where `0` means download it but do not track it as an episode.

**The whole release is downloaded.** A magnet link carries no file list, so the
checkboxes decide which files become *episodes*; every file in the pack arrives
regardless. Files you leave unchecked are removed once the torrent completes,
if `prune_unselected` is enabled. If you tick the wrong boxes, the wrong files
are kept — that is your call.

Adopted seasons sit outside the airing pipeline: the release was chosen by
hand, so there is nothing to hunt for and nothing to learn. They go straight to
*downloading*, then *ready to watch*, and are deleted after watching like any
other episode. They appear under **Complete** rather than Airing.

Cover art comes from AniList: the entry URL carries the AniList id, and
AniList serves the same poster the SeaDex page shows. If the lookup fails the
adoption still succeeds, without a poster.

From the command line, `-adopt` is a dry run that prints the plan and changes
nothing; add `-adopt-confirm` to perform it.

## Watch signal

`POST /api/watched` marks an episode watched. It is not tied to any particular
player — anything that can POST JSON can send it.

```json
{"path": "/anywhere/Show - E09.mkv"}
```

Only the **base name** is used for matching, so the client's directory layout
is irrelevant. You can also be explicit:

```json
{"show_id": 1, "episode": 9}
```

An example mpv script is in [`scripts/mpv/`](scripts/mpv/). It only reports
files under a configured root, so using mpv for other media will not spam the
server. Failed posts are spooled and retried on the next start, and also raise
a notification. A missed signal leaves a file on disk, which is the safe
direction.

Media servers are supported directly: `POST /api/webhook` accepts watch
events from Jellyfin, Plex and Emby in their native payload shapes — point
the server's webhook at it and no plugin or script is needed. See
[`API`](docs/API.md) for the payload formats.

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `-db` | `kishizu.db` | SQLite database path |
| `-config` | `kishizu.yaml` | Configuration file |
| `-serve` | — | Address for the web UI, e.g. `127.0.0.1:8098` |
| `-library` | `/media/anime` | Library root, as kishizu sees it |
| `-staging` | `/downloads/anime` | Staging root, as kishizu sees it |
| `-keep` | `2` | Recently watched episodes to keep on disk |
| `-interval` | `5m` | Poll interval |
| `-dry-run` | `true` | Decide but do not download |
| `-downloader` | `transmission` | Torrent client: `transmission` or `qbittorrent` |
| `-transmission` | — | Transmission RPC endpoint |
| `-qbittorrent` | — | qBittorrent WebUI URL |
| `-qbittorrent-user` | — | qBittorrent WebUI username |
| `-qbittorrent-pass` | — | qBittorrent WebUI password |
| `-notifier` | `ntfy` | Notification backend: `ntfy`, `gotify` or `none` |
| `-ntfy` | — | ntfy topic URL; empty disables |
| `-gotify` | — | Gotify server URL |
| `-gotify-token` | — | Gotify app token |
| `-debug` | `false` | Verbose logging of every decision |
| `-version` | — | Print the version and exit |
| `-seed` | — | Insert the shows from the config file |
| `-list` | — | List tracked shows with next episode and air dates |
| `-show` | — | Only run this show (substring match) |
| `-train` | — | Train a show (substring match) |
| `-ep` | `0` | Episode to train against (0 = next unwatched) |
| `-infer` | — | Derive group offsets from air dates instead of training |
| `-adopt` | — | Adopt a finished season from a releases.moe URL |
| `-adopt-episodes` | — | Episode numbers to adopt, one per file |
| `-adopt-confirm` | `false` | With `-adopt`: perform it instead of printing the plan |
| `-backfill-slugs` | — | Attach animeschedule slugs from the mapping file |
| `-slugs` | `slugs.yaml` | Name → slug mapping used by `-backfill-slugs` |
| `-reconcile` | — | File completed downloads in staging once, then exit |

A flag overrides the config file only when you actually pass it. Every flag has
a default, so applying them unconditionally would let a default silently
overwrite a configured value.

## API

The web UI is a client of the JSON API; anything it can do, you can do with
`curl`. See [`API`](docs/API.md) for every endpoint.

A few worth knowing:

| Endpoint | Purpose |
|---|---|
| `GET /api/summary` | Dashboard widget: counts, next air time, version |
| `GET /healthz` | Liveness, for container healthchecks |
| `GET /api/version` | Build version and Go toolchain |
| `POST /api/watched` | Mark an episode watched |
| `GET /api/timetable` | Browse the season, with `?q=` to filter |

## Security

**There is no authentication.** Anyone who can reach the port has full control:
they can add and remove shows, mark episodes watched, and trigger downloads.

This is a deliberate trade-off for a single-user tool on a trusted network. It
is why the default bind address is `127.0.0.1`. If you expose kishizu beyond
localhost, put it behind something that authenticates — a reverse proxy with
auth, or a private network such as a VPN or tailnet. Do not port-forward it to
the internet.

See [`SECURITY`](docs/SECURITY.md) for how to report a vulnerability.

## License

[MIT](LICENSE)
