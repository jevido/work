# syntax=docker/dockerfile:1.7

# The sync server behind work.jevido.app.
#
# The build context is the repository root rather than this directory, and that
# is not an accident: the monorepo is one Go module, so the manifest that
# resolves this program's imports is go.mod at the root, and packages/ops --
# the one merge implementation both sides run -- sits outside this tree.
#
# Nothing about the desktop app is in this image. The dependency runs one way
# -- the server reads packages/ops, and the app imports nothing from here -- so
# the only things copied are the root manifest, that package, and this service.
#
# The website is, though, and it is built here rather than uploaded as a CI
# artifact: `docker build -f services/sync/Dockerfile .` on any machine then
# produces the thing that runs in production. A deploy that depends on a file
# some other job happened to leave behind is a deploy nobody can reproduce
# locally.

FROM golang:1.26-alpine AS build

WORKDIR /src

# The manifest first, before any source. This layer changes only when a
# dependency does, so the module download below is cached across every ordinary
# code change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# This service and everything shared. packages/ is copied whole rather than
# named package by package: this image once listed only packages/ops, and the
# day the server started importing packages/site it failed here instead of in a
# test. The directory is small and mostly Go; the two TypeScript packages in it
# cost a few kilobytes and buy an image that cannot go stale this way again.
#
# Still not the whole repository: the desktop app, its frontend and every asset
# stay out of a build for a binary that uses none of it.
COPY packages /src/packages
COPY services/sync /src/services/sync

WORKDIR /src/services/sync

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

# The repository's own shape, kept inside the image: the website's aliases
# point at ../../packages, so the two have to sit in the same relation here as
# they do on disk or nothing resolves.
WORKDIR /src/apps/website

# The manifests alone first, for the same reason the Go stage copies go.mod
# before any source: this layer changes only when a dependency does.
COPY apps/website/package.json apps/website/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci

COPY apps/website/ ./

# The two shared packages, at the path the aliases name. `@mindmap/*` and
# `@ui/*` resolve to ../../packages/* in both the website's vite config and its
# tsconfig, which from this working directory is /src/packages. Without them
# the stage below fails on "Cannot find module '@mindmap/renderer'".
#
# Only these two directories come over. They import nothing outside themselves:
# geometry, painting, and a pointer with two numbers, which is what makes them
# shareable with a page that has no document model at all.
COPY packages/mindmap /src/packages/mindmap
COPY packages/ui /src/packages/ui

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
COPY --from=site /src/apps/website/dist /srv/site
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
