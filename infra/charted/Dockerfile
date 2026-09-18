# syntax=docker/dockerfile:1.7

# Charted: the documentation site, binary and reader in one image.
#
# Two directories, the way the repository is split: services/charted is the
# server, apps/charted is the page it serves. The build context is the
# repository root because the monorepo is one Go module and the manifest that
# resolves this program's imports is go.mod at the top.

FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# packages/ comes whole, for the reason infra/sync/Dockerfile gives: naming the
# ones in use today is how an image breaks the day a service imports one more.
COPY packages ./packages
COPY services/charted ./services/charted

WORKDIR /src/services/charted

# CGO off makes a static binary, which is what lets the final stage be a bare
# runtime with no libc to match.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags='-s -w' \
        -o /out/charted .

# The tests that need no database, run where the artifact is built rather than
# trusting that somebody ran them. The renderer is the one that matters here:
# it decides what HTML every page is stored as.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go test ./...

# The reader, built with the server that serves it.
FROM node:22-alpine AS site

WORKDIR /src/apps/charted

COPY apps/charted/package.json apps/charted/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci

COPY apps/charted/ ./

RUN npm run check && npm run build

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 10001 charted

COPY --from=build /out/charted /usr/local/bin/charted
COPY --from=site /src/apps/charted/dist /srv/site
ENV CHARTED_SITE_DIR=/srv/site

USER charted

ENV PORT=8081
EXPOSE 8081

# /v1/health does a database round trip, so a passing check means it can serve
# rather than that the process is alive. start-period covers the migrations,
# which run before the listener opens.
HEALTHCHECK --interval=15s --timeout=5s --start-period=40s --retries=3 \
    CMD wget --quiet --spider --tries=1 "http://127.0.0.1:${PORT}/v1/health" || exit 1

ENTRYPOINT ["/usr/local/bin/charted"]
