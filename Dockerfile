# kishizu — seasonal anime downloader.
#
# Multi-stage: build the static Go binary, then ship it on a minimal base.
# The binary is self-contained (no CGO), so the runtime image needs nothing
# but CA certificates for outbound HTTPS to Nyaa and animeschedule.

FROM golang:1.27 AS build

WORKDIR /src

# Dependencies first, so a code change does not re-download them.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Version is injected so the binary can report what build it is. Without it
# a pinned image tag is the only clue, and a tag can be re-pushed.
ARG VERSION=dev

# CGO off gives a static binary that runs on any base image.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-X github.com/Ebonhawk3829/kishizu/internal/version.Version=${VERSION}" \
    -o /out/kishizu ./cmd/kishizu

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata curl

COPY --from=build /out/kishizu /usr/local/bin/kishizu

# Data lives here: the SQLite database and the show seed file. Mount a
# volume at /data to persist them.
RUN mkdir -p /data && chown 1001:1001 /data
WORKDIR /data

# Run unprivileged. Override with `user:` in compose to match your media
# user's uid/gid, so files land with ownership your other containers accept.
USER 1001:1001

# 8098 is the web UI and API.
EXPOSE 8098

# Dry-run by default: the container polls and logs decisions but hands
# nothing to the downloader until -dry-run=false is passed explicitly.
ENV KISHIZU_DB=/data/kishizu.db
ENV KISHIZU_CONFIG=/data/kishizu.yaml

# A container healthcheck. /healthz is deliberately shallow — it does not
# touch the database or the network, so a slow upstream cannot mark a
# correctly-running container unhealthy.
HEALTHCHECK --interval=60s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8098/healthz || exit 1

# Bound to localhost by default. There is no authentication, so exposing
# this to a network exposes full control of the tool. Override with
# `-serve 0.0.0.0:8098` only on a network you trust (a VPN or tailnet).
ENTRYPOINT ["/usr/local/bin/kishizu"]
CMD ["-db", "/data/kishizu.db", "-config", "/data/kishizu.yaml", \
     "-serve", "127.0.0.1:8098", "-dry-run"]
