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

| Stage | What happens |
|---|---|
| **Declare** | Add shows by browsing the season, pasting an animeschedule.net URL, or listing them in the config file |
| **Hunt** | Polls the indexer per show, parses each release, matches it to a show and episode |
| **Grab** | Hands the best matching release to your torrent client, ranked by your group order |
| **File** | Renames it into your library, ready for your player |
| **Watch** | Anything that can POST JSON tells kishizu when you finish an episode |
| **Clean** | Deletes watched episodes per the delete policy: immediately, after a delay, or never |

Air times come from animeschedule.net: the daily refresh stores each tracked
show's next episode and when it airs, and the seasonal browse list is
refreshed weekly. A watch signal also re-reads just that show's page, so a
season the site marks Finished reclassifies when you finish watching it, not
a day later. Everything is cached locally, so kishizu keeps working when the
site is down.

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

Releases follow semver. Patch releases are safe to take unread. Minor releases
may add configuration keys; existing ones keep working. A major release may
change behaviour, and the release notes will say so.

What it does not do:

- **Hunt a back catalogue.** It tracks shows week by week while they air. A finished season can be [adopted](#adopting-a-finished-season) instead.
- **Download batches or season packs** as part of the airing pipeline. One episode at a time.
- **Support multiple users.** No auth, no permissions model. See [Security](#security).

If you need something general-purpose today, use
[autobrr](https://github.com/autobrr/autobrr) or
[Sonarr](https://sonarr.tv/) with an anime indexer.

## Requirements

- A torrent client with a remote API: **Transmission** or **qBittorrent**.
- Go 1.27+ if building from source. Docker needs nothing else.
- Optionally, a notification service: **ntfy** or **Gotify**.

kishizu runs dry by default, so you can point it at your library and see what
it decides before it downloads anything.

## Installation

### Docker Compose

An example deployment using Transmission as the client. Adjust the image tag,
the client settings and the paths to your setup — qBittorrent works the same
way (see the [flags](#flags) table).

```yaml
services:
  kishizu:
    image: ghcr.io/ebonhawk3829/kishizu:latest
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
```

Then open **http://localhost:8098** and set the rest from the UI: the
Transmission RPC endpoint, the library and staging roots, and `dry_run: false`
to switch downloading on. Settings live in `/data/kishizu.yaml`, which the UI
edits — there are no flags for them.

Three things about that compose file:

**Mount `./media:/media` as one mount, not two.** The final move from staging
to library is a rename, which cannot cross mount points. Separate mounts for
library and staging fail with `invalid cross-device link`.

**`-serve` must be `0.0.0.0:8098` inside the container.** kishizu binds to
localhost by default, which Docker's port mapping cannot reach. The host side
of the mapping (`127.0.0.1:8098:8098`) keeps it off your network.

**Dry-run is the default.** Set `dry_run: false` in the config file (or the
UI) to download for real.

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
  delete: immediate              # immediate | after | off
  # delete_after: 7d             # with delete: after — hours (48h) or days (7d)
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
```

See [`kishizu.yaml.example`](kishizu.yaml.example) for the annotated version.

If you are moving to kishizu mid-season and want to pre-seed the database,
configure this file first, then run with `-seed` so shows land with the paths
and policy you intend.

### Settings in the UI

**Settings** in the web UI edits this file. Paths and the downloader are
captured at startup and need a restart; everything else takes effect on save.

### Secrets

The torrent client password and the notification token are stored in this file
in plain text. To keep them out, supply them by environment variable instead:

| Variable | Replaces |
|---|---|
| `KISHIZU_QBITTORRENT_PASS` | `downloader.qbittorrent_pass` |
| `KISHIZU_GOTIFY_TOKEN` | `notifier.gotify_token` |

An environment variable takes precedence when set and non-empty, and is never
written back into the file. The UI masks secrets and never displays them.

### Notifications

With a notifier configured, kishizu sends alerts for things that otherwise sit
silently:

| Alert | When |
|---|---|
| Downloading | An episode was handed to the torrent client. |
| Download stalled | An episode has been downloading for over 48 hours with no file. Sent once per episode. |
| Downloader unreachable | The torrent client did not respond. Sent once per outage, with a follow-up when it returns. |
| Needs training | A show has started airing but has no known group offsets. Sent once per show. |

### Naming

| Preset | Layout |
|---|---|
| `kishizu` | `<library>/<Show>/<Show> - E09.mkv` |
| `sonarr` | `<library>/<Show>/Season 01/<Show> - S01E09.mkv` |
| `plex` | `<library>/<Show>/Season 01/<Show> - s01e09.mkv` |
| `custom` | your own pattern |

A custom pattern uses `{show}`, `{season}`, `{season:2}`, `{episode}`,
`{episode:2}`, and must contain `{show}` and an episode placeholder.

## Adding shows

Everything lives under **Add a show**.

**Browse the season.** Open **Browse this season** for the cached timetable.
Pick a show and the slug fills in the cover art and alternative names
automatically. The list shows romaji or English names (toggle in the
panel or set `browse.title` in the config); the filter matches both either way.

The browse list is cached on disk — it works even when animeschedule is down.
A background job refreshes it weekly; a fresh container fetches once at
startup so the panel is never empty. **Refresh** in the panel forces an
immediate update.

**Paste a URL.** An animeschedule.net URL (or a bare slug) does the same thing:

```
https://animeschedule.net/anime/re-zero-kara-hajimeru-isekai-seikatsu-4
```

A plain show name is not accepted here — the slug is what carries the season
length, cover art and air dates. To add a show by name, list it in the config
file and run `-seed`. A show seeded without a slug gets its air dates once you
attach one with `-backfill-slugs`.

Only romanised names are imported as aliases. Japanese names cannot appear in
release titles, and short abbreviations risk false matches.

## How matching works

Release groups disagree about episode numbering. For one cour of a long-running
show, one group posts `E01` while another posts `E41`, and both are the same
episode. kishizu stores an **offset per release group**, so `[VARYG] Show - 47`
and `[Erai-raws] Show - 07` can both resolve to episode 7.

Offsets are learned either by training (confirm the parse of a few releases,
one per numbering convention) or by `-infer`, which derives them from the
structure of each group's numbering. Both are reviewable and resettable per
show in the UI.

Training teaches the per-group episode offset and the title vocabulary — a
mapping from tokens in a release title to canonical values, so every future
release using that spelling is readable. Quality policy (resolution floor,
group order, etc.) is configured globally and applies to every show the same
way.

A show starts hunting once it has at least one known group offset. Untrained
shows appear as *needs training* in the UI. A show whose first episode has not
yet aired appears as *upcoming*.

### Season end

The schedule page's own status decides when a season is over. When it reports
*Finished*, the show stops polling and moves to the **Complete** section of the
UI. A watch signal picks this up within seconds of you finishing the last
episode; the daily refresh covers shows nobody is watching. A show on hiatus
keeps polling.

## Episode states

| State | Meaning |
|---|---|
| *upcoming* | The season has not started. |
| *hunting* | Aired, within 72 hours, no release grabbed yet. Polled every 3 minutes. |
| *downloading* | Handed to the torrent client, not on disk yet. Not polled. |
| *ready to watch* | On disk, waiting for you. |
| *missing* | Was on disk and is not now. Needs a decision: re-grab or mark watched. |
| *no release found* | The 72-hour window closed with nothing grabbed. |
| *up to date* | Watched or deleted. |

## Adopting a finished season

For a season that has already finished, kishizu can adopt a release from
[releases.moe](https://releases.moe) (SeaDex), a community index of the
highest-quality release for a given anime.

Open **Adopt a finished season** and paste the entry URL. kishizu lists every
file in the release and proposes which are episodes. Each file gets a checkbox
and an episode dropdown for you to confirm or correct before anything
downloads. Extras (NCOP, NCED, OVA, specials) are unchecked by default.

From the command line:

```sh
./kishizu -db kishizu.db -adopt "https://releases.moe/112124/"
```

`-adopt` is a dry run that prints the plan; add `-adopt-confirm` to perform it.
`-adopt-episodes` overrides the proposed episode numbers, one per file, where
`0` means download but do not track as an episode.

The entire release is always downloaded — magnet links carry no file list, so
the checkboxes control which files become tracked episodes, not which files
arrive. Unchecked files are removed after the torrent completes if
`prune_unselected` is enabled.

Adopted seasons appear under **Complete** and follow the same watch/delete
cycle as airing episodes. Cover art is fetched from AniList using the id in
the URL.

## Watch signal

`POST /api/watched` marks an episode watched. Anything that can POST JSON can
send it.

```json
{"path": "/anywhere/Show - E09.mkv"}
```

Only the **base name** is used for matching, so the client's directory layout
does not matter. You can also be explicit:

```json
{"show_id": 1, "episode": 9}
```

An example mpv script is in [`scripts/mpv/`](scripts/mpv/). It only reports
files under a configured root, so using mpv for other media does not trigger
false signals. A missed signal leaves a file on disk, which is the safe
direction.

Media servers are supported directly: `POST /api/webhook` accepts watch events
from Jellyfin, Plex and Emby in their native payload shapes — point the
server's webhook at it and no plugin or script is needed. See
[`API`](docs/API.md) for the payload formats.

## Flags

Flags cover where the database is, where the config file is, and which mode to
run. Every server setting is configured in `kishizu.yaml` and editable from
the web UI.

| Flag | Default | Purpose |
|---|---|---|
| `-db` | `kishizu.db` | SQLite database path |
| `-config` | `kishizu.yaml` | Configuration file |
| `-serve` | — | Address for the web UI, e.g. `127.0.0.1:8098` |
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

## API

The web UI is a client of the JSON API; anything it can do, you can do with
`curl`. See [`API`](docs/API.md) for every endpoint.

A few worth knowing:

| Endpoint | Purpose |
|---|---|
| `GET /api/summary` | Dashboard widget: counts, next air time, version |
| `GET /api/schedule` | What airs this week: every show's next episode in a window |
| `GET /healthz` | Liveness, for container healthchecks |
| `POST /api/watched` | Mark an episode watched |
| `POST /api/watched/verify` | Report whether an episode is marked watched |
| `GET /api/timetable` | Browse the season, with `?q=` to filter |

## Security

**There is no authentication.** Anyone who can reach the port has full control:
they can add and remove shows, mark episodes watched, and trigger downloads.

The default bind address is `127.0.0.1`. If you expose kishizu beyond
localhost, put it behind a reverse proxy with auth, or a private network such
as a VPN or tailnet. Do not port-forward it to the internet.

See [`SECURITY`](docs/SECURITY.md) for how to report a vulnerability.

## License

[MIT](LICENSE)
