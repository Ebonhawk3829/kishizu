# kishizu HTTP API

The web UI is a client of this API. Anything the UI can do, you can do with
`curl`.

**There is no authentication.** Anyone who can reach the port has full control.
Bind to localhost or a private network — see [SECURITY.md](../SECURITY.md).

Base URL is wherever you started `-serve`, e.g. `http://127.0.0.1:8098`.

Errors come back as JSON with an `error` field and an appropriate status code.

---

## Health and version

### `GET /healthz`

Liveness. Deliberately shallow — it does not touch the database or the network,
so a slow upstream cannot mark a correctly-running container unhealthy.

```json
{"status":"ok","version":"v1.0.0"}
```

### `GET /api/version`

```json
{"version":"v1.0.0","go":"go1.27.1"}
```

---

## Shows

### `GET /shows`

Every tracked show.

```json
[
  {
    "id": 1,
    "name": "Tomb Raider King",
    "next": 10,
    "max": 12,
    "aliases": ["Dogul Wang"],
    "offsets": {"VARYG": 0},
    "trained": true,
    "adopted": false,
    "image_url": "/art/abc123.jpg",
    "cadence": 3,
    "next_schedule": {"episode": 10, "airs_at": "...", "status": "aired"},
    "state": "hunting",
    "needs_attention": false,
    "downloaded": 2,
    "watched": 7,
    "deleted": 0
  }
]
```

`state` is one of: `upcoming`, `needs training`, `hunting`, `downloading`,
`ready to watch`, `missing`, `no release found`, `up-to-date`, `complete`.

`adopted` is true for a season taken from releases.moe. Such a show is never
trained and never polled, so the UI suppresses training for it.

### `POST /api/shows`

Add a show. `name` accepts either a plain name or an animeschedule.net URL (or
a bare slug). A URL is better: the slug is an exact identity, and the page
carries the season length, cover art and every alternative name.

```json
{"name":"https://animeschedule.net/anime/re-zero-kara-hajimeru-isekai-seikatsu-4",
 "aliases":["ReZero 4"],
 "max_episode":0}
```

### `DELETE /api/shows`

```json
{"id":1}
```

Removes the show with its aliases, offsets, episodes and dedupe entries.

---

## Watch signal

### `POST /api/watched`

Mark one episode watched. This is the endpoint a media player posts to.

Two ways to identify the episode:

```json
{"path":"/anywhere/Tomb Raider King - E09.mkv"}
```

```json
{"show_id":1,"episode":9}
```

The first matches the **base name** against tracked episodes, so the client's
directory layout does not matter — only the filename. The second is explicit
and takes precedence.

Marking watched also sweeps: files for watched episodes are deleted per the
delete policy (`immediate` by default; `after` respects the delay; `off`
deletes nothing).

### `POST /api/watched-up-to`

Mark everything up to an episode watched.

```json
{"show_id":1,"up_to":9,"force":false}
```

`force` overrides the in-flight guard, for a download that got stuck and will
never complete.

### `POST /api/webhook`

Watch events from media servers: Jellyfin, Plex and Emby. Same effect as
`/api/watched`, translated from each server's payload shape.

Jellyfin and Emby post JSON:

```json
{"Event":"item.markplayed","Item":{"SeriesName":"Tomb Raider King","IndexNumber":9}}
```

Plex posts `multipart/form-data` with a `payload` field holding JSON:

```json
{"event":"media.scrobble","Metadata":{"grandparentTitle":"Tomb Raider King","index":9}}
```

A plain hand-written POST also works:

```json
{"show":"Tomb Raider King","episode":9}
```

Show names match canonical names and aliases, case-insensitively. Events that
are not watch completions (playback started, paused, …) are answered 200 with
`{"marked":false}` rather than an error. An unknown show or an unusable
payload is answered 422 — nothing is guessed.

Point the media server's webhook at `http://<kishizu-host>:8098/api/webhook`.
In Jellyfin/Emby the plugin is "Webhook"; in Plex it is "Webhooks" under
Settings → Extras. Note kishizu has no authentication, so only expose the
port on a network you trust.

---

## Episode state

### `POST /api/set-state`

Force an episode into a state, for episodes obtained outside kishizu.

```json
{"show_id":1,"episode":9,"state":"downloaded","path":""}
```

Allowed: `wanted`, `downloading`, `downloaded`, `blocked`. Use `/api/watched`
for watched. Refuses to move a terminal episode (watched/deleted) backwards —
that would resurrect something already consumed.

### `POST /api/unlatch`

Reset an episode to `wanted` so it can be re-grabbed. The seen-infohash check
means the next grab skips the release you already have and takes the next best
one.

```json
{"show_id":1,"episode":9}
```

---

## Training

Training teaches the per-group episode offsets and the title vocabulary. A show
is not polled until it has at least one offset.

| Endpoint | Purpose |
|---|---|
| `POST /api/train/start` | Begin a session for a show |
| `GET /api/train/state` | Current session state |
| `POST /api/train/inspect` | Parse one title into attributes |
| `POST /api/train/grade` | Record a correction |
| `POST /api/train/accept-all` | Accept the proposed parses |
| `POST /api/train/commit` | Save, then re-verify against the live feed |
| `POST /api/train/reset` | Clear a show's offsets |
| `POST /api/train/vocab` | Teach a token's meaning |

---

## Adopting a finished season

### `POST /api/adopt/preview`

```json
{"url":"https://releases.moe/112124/"}
```

Returns the release, every file in it, and a proposed episode number per file.

### `POST /api/adopt`

```json
{"url":"https://releases.moe/112124/","title":"DanMachi III","info_hash":"...",
 "files":[{"name":"01.mkv","include":true,"episode":1}]}
```

**The whole release is downloaded.** A magnet link carries no file list, so
`include` decides which files become *episodes*, not which files arrive. Files
you leave out are downloaded with the pack and then removed once the torrent
completes, if `prune_unselected` is enabled.

---

## Browsing

### `GET /api/timetable`

The seasonal timetable, from a cached snapshot.

| Query | Effect |
|---|---|
| `q` | Filter by title or slug substring |
| `refresh=1` | Force a re-fetch, ignoring the cache |

```json
{"fetched":"...","count":2,
 "entries":[{"slug":"...","title":"...","image":"...","airs_at":"...","tracked":false}]}
```

`tracked` marks shows you already follow. Returns 501 when no cache is
configured.

---

## Configuration

### `GET /api/config`

The effective configuration, with secrets masked. Also returns the available
choices for each enum.

### `POST /api/config`

Write the configuration back to the file. Send the same shape you got from GET.

Secrets: a masked or empty value means **preserve the existing one**, not clear
it. The UI never displays a credential, so it cannot send one back.

Returns `restart_required: true` — paths and the downloader are captured at
startup.

---

## Stats and diagnostics

### `GET /api/summary`

Shaped for a dashboard widget.

```json
{"status":"3 to watch","ready":3,"downloading":1,"hunting":2,"missing":0,
 "upToDate":false,"next":{"show":"...","episode":18,"airs_at":"..."},
 "version":"v1.0.0","downloader_url":"http://transmission:9091/transmission/rpc"}
```

`downloader_url` is the torrent client's web address, for linking a user to
the client's own UI when a download needs manual attention. Empty when the
client has no web UI.

### `GET /api/stats`

Per-show counts: `downloading`, `downloaded`, `watched`, `deleted`.

### `GET /api/debug` / `POST /api/debug`

Read or toggle verbose logging at runtime, without a restart. Debug mode logs
every decision with its reason — this is what you want when a release is not
being grabbed.
