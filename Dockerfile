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

# CGO off gives a static binary that runs on any base image.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/kishizu ./cmd/kishizu

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata curl

COPY --from=build /out/kishizu /usr/local/bin/kishizu

# Data lives here: the SQLite database and the show seed file. Mount a
# volume at /data to persist them.
RUN mkdir -p /data && chmod 0755 /data
WORKDIR /data

# 8098 is the web UI and API. Bound to the tailnet address by compose.
EXPOSE 8098

# Dry-run by default: the container polls and logs decisions but hands
# nothing to Transmission until -dry-run=false is passed explicitly.
ENV KISHIZU_DB=/data/kishizu.db
ENV KISHIZU_CONFIG=/data/shows.yaml

ENTRYPOINT ["/usr/local/bin/kishizu"]
CMD ["-db", "/data/kishizu.db", "-config", "/data/shows.yaml", \
     "-serve", "0.0.0.0:8098", "-dry-run"]
