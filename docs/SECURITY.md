# Security Policy

## Reporting a vulnerability

**Do not open a public issue for a security problem.**

Use GitHub's private vulnerability reporting:
<https://github.com/Ebonhawk3829/kishizu/security/advisories/new>

Include what you can: what the issue is, how to reproduce it, and what an
attacker could do with it. You should get an acknowledgement within a few days.

## Scope

Things that count as vulnerabilities:

- The web UI or API being reachable by someone who should not reach it.
- A path traversal or similar that lets a request delete or read files outside
  the configured library and staging directories.
- A crafted release title, show name or filename that causes kishizu to act
  outside its intended boundaries.
- Credentials for the torrent client or notification service being exposed in
  logs, the API, or the UI.

## Out of scope, by design

These are known and documented properties of the tool, not vulnerabilities:

**There is no authentication.** The web UI and API have no login, no tokens and
no permissions model. Anyone who can reach the port has full control: they can
add and remove shows, mark episodes watched, and trigger downloads.

This is a deliberate trade-off for a single-user tool on a trusted network. It
is why the default bind address is `127.0.0.1`. If you expose kishizu beyond
localhost, put it behind something that authenticates — a reverse proxy with
auth, or a private network such as a VPN or tailnet. Do not port-forward it to
the internet.

**The API is unauthenticated and unthrottled.** Same reason, same mitigation.

**kishizu fetches from third parties over the network.** Nyaa, animeschedule.net,
releases.moe and AniList. It trusts what they return. A compromised or hostile
upstream could supply a crafted title or filename; the parser is deliberately
dumb, and the matcher fails closed when uncertain, but this is not a hardened
boundary.

**Secrets are stored in the configuration file.** The torrent client password
and the notification token live in the configuration file in plain text. The web UI masks
them and never writes them back, but they are on disk. Protect the file with
filesystem permissions.

If you would rather not have them on disk at all, supply them by environment
variable instead:

| Variable | Replaces |
|---|---|
| `KISHIZU_QBITTORRENT_PASS` | `downloader.qbittorrent_pass` |
| `KISHIZU_GOTIFY_TOKEN` | `notifier.gotify_token` |

An environment variable wins over the file when set and non-empty, and is never
written back into it. A variable that is set but blank is treated as unset, so
an empty variable in a compose file cannot silently clear a credential you
configured deliberately.

## If you deploy it

- Bind to localhost, or to a private network interface. Not `0.0.0.0` on a
  public host.
- Restrict the configuration file to the user kishizu runs as.
- Run as a non-root user with access only to the library and staging paths.
- Remember that deletion is scoped to the library root: a request cannot delete
  outside it, but anything inside it is fair game to anyone who can reach the
  API.
