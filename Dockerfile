# syntax=docker/dockerfile:1.7

# The sync server behind work.jevido.app.
#
# The build context is the repository root rather than server/, and that is
# not an accident: server/ is its own Go module and replaces dev.jevido/work
# with ../ so it can use internal/ops, the one merge implementation both sides
# run. A context rooted at server/ cannot see that directory, so the module
# would not resolve.
#
# Nothing about the desktop app is in this image. The dependency runs one way
# -- the server reads internal/ops, and the app imports nothing from server/ --
# so the only thing copied out of the parent module is that one package.
#
# The web viewer is, though, and it is built here rather than uploaded as a CI
# artifact: `docker build .` on any machine then produces the thing that runs
# in production. A deploy that depends on a file some other job happened to
# leave behind is a deploy nobody can reproduce locally.

FROM golang:1.25-alpine AS build

WORKDIR /src

# Both modules' manifests first, before any source. This layer changes only
# when a dependency does, so the module download below is cached across every
# ordinary code change. The parent's go.mod has to be here too: the replace
# directive points at it, and nothing resolves until it can be read.
COPY go.mod go.sum ./
COPY server/go.mod server/go.sum ./server/

WORKDIR /src/server
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Only the shared package comes across from the parent module. Copying the
# whole repository would put the frontend, the Wails build and every asset
# into the build context for a binary that uses none of it.
COPY internal/ops /src/internal/ops
COPY server /src/server

# CGO off makes a static binary, which is what lets the final stage be a bare
# runtime with no libc to match. -trimpath keeps build paths out of it and
# -s -w drops the symbol table: this binary is never debugged in production,
# it is redeployed.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags='-s -w' \
        -o /out/work-server .

# Run the API's tests in the image build rather than trusting that someone ran
# them. They need no database -- the HTTP layer is tested against an in-memory
# store -- so this costs seconds and catches a broken push before it is
# serving. The store's own Postgres tests skip themselves without
# TEST_DATABASE_URL, which is the right thing here.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go test ./...

# The viewer, built with the server that serves it.
#
# Its own stage, so that a change to the Go source does not reinstall npm
# dependencies and a change to the viewer does not rebuild the binary.
FROM node:22-alpine AS site

WORKDIR /web

# The manifests alone first, for the same reason the Go stage copies go.mod
# before any source: this layer changes only when a dependency does.
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci

COPY web/ ./

# The map, which is the desktop app's and is shared rather than copied.
#
# `@mindmap/*` resolves to ../frontend/src/lib/mindmap in both the viewer's
# vite config and its tsconfig, so with WORKDIR /web it has to land at
# /frontend/src/lib/mindmap -- outside this stage's working directory, which
# looks wrong and is exactly what the alias asks for. Without it the stage
# below fails on "Cannot find module '@mindmap/renderer'".
#
# Only this one directory comes over. It imports nothing outside itself: it is
# pure geometry and painting, which is what makes it shareable with a page
# that has no document model at all.
COPY frontend/src/lib/mindmap /frontend/src/lib/mindmap

# Typechecked here as well as in CI, for the same reason the stage above runs
# go test: the check that gates an artifact belongs beside the artifact, not in
# a job that could be skipped or reordered.
RUN npm run check && npm run build

FROM alpine:3.22

# ca-certificates for a Postgres connection that uses TLS. A managed database,
# or one reached over anything but a private network, will; without these the
# handshake fails with an error that reads like a configuration mistake.
# wget comes with busybox and is what the healthcheck below uses -- a static
# binary on a distroless base would be smaller, but it would also have no way
# to answer "is it up?" without shipping a second binary to ask.
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 10001 work

COPY --from=build /out/work-server /usr/local/bin/work-server

# The viewer, and where the server is told to look for it. Copied before USER
# below, so it lands root-owned and world-readable -- which is what is wanted,
# because the server only ever reads it.
COPY --from=site /web/dist /srv/site
ENV WORK_SITE_DIR=/srv/site

USER work

# The server reads PORT and defaults to 8080. This is documentation for
# whatever is publishing it; the value that matters is the one in the
# environment.
ENV PORT=8080
EXPOSE 8080

# /v1/health is unauthenticated and does a database round trip, so a passing
# check means the server can actually serve rather than merely that the
# process is alive. start-period covers the migrations, which run before the
# listener opens.
HEALTHCHECK --interval=15s --timeout=5s --start-period=40s --retries=3 \
    CMD wget --quiet --spider --tries=1 "http://127.0.0.1:${PORT}/v1/health" || exit 1

ENTRYPOINT ["/usr/local/bin/work-server"]
