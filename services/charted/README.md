# Charted

The documentation site. Go, Svelte and Postgres, in one binary and one image.

It is what `/documentation` in the desktop app publishes to: a page is written
over HTTP, rendered once on arrival, and served from the row it was stored in.

## Why not files on disk

The usual shape for a documentation site is markdown committed beside the code
it describes, rendered at build time, deployed as static files. That is the
right shape when the person writing the docs is the person pushing the
repository.

Here they are not. Documentation mode runs on somebody's machine, reads a
project that has just finished a piece of work, and publishes — to a site that
is already running, somewhere else. A write has to land somewhere both ends can
reach, which is a database, and the site has to serve the new page without a
rebuild, which is a server. That is the whole design rationale; everything below
follows from it.

## The shape

```
main.go              Config, migrations, the listener, the static reader
api/                 HTTP: public reads, token-gated writes
render/              Markdown to HTML, plain text, headings, links
store/               Postgres: spaces, pages, search, the link graph
store/migrations/
```

The page this serves is [`apps/charted`](../../apps/charted/README.md): a static
Svelte bundle that reads the API below. It lives over there because a person
opens it, and this lives here because nobody ever looks at it — the same split
as the sync server and the website it hands out.

A **space** is a section of the site — one product, one area. A **page** is
markdown inside a space, addressed as `/<space>/<slug>`, where the slug may
contain slashes. Both are rows.

Markdown is rendered **once, on write**, and the HTML is stored beside the
source. Rendering is deterministic, a page is read far more often than written,
and a renderer upgrade should be a visible re-render rather than a silent
difference between two requests. Raw HTML is disabled: pages are written by an
agent summarising somebody's repository, which is the one author who can be
talked into emitting a `<script>` by the contents of a file.

## The API

Reading is public. Writing needs `Authorization: Bearer $CHARTED_TOKEN`, and
without a token set the write side refuses everything rather than falling open.

```
GET    /v1/health
GET    /v1/nav                       every space and its pages, in order
GET    /v1/pages/{space}/{slug...}   one page: html, markdown, contents
GET    /v1/search?q=                 full-text, ranked, with excerpts

PUT    /v1/spaces/{space}            title, summary, position
PUT    /v1/pages/{space}/{slug...}   title, description, markdown, position
DELETE /v1/pages/{space}/{slug...}
GET    /v1/links/broken              every internal link with no page behind it
```

A `PUT` answers `200` with the broken links **this page** now has:

```json
{ "space": "studio", "slug": "invites", "broken": [{ "space": "studio", "slug": "roles", "label": "roles" }] }
```

Reported, not refused. Documentation is written in an order — the overview
before the page it points at — and a writer that cannot save the first page
until the second exists writes the second page badly.

## Search

One Postgres index over title, description and body, weighted so a title match
beats a body mention, with `websearch_to_tsquery` so quoted phrases and
`-exclusions` typed into the box mean what they look like. Excerpts come from
`ts_headline`. There is no second search system and nothing to keep in step.

## Links

Every internal link a page makes is a row, written in the same transaction as
the page. The checker is a left join against `pages`, so a broken link is found
by the write that could have created it rather than by a crawl afterwards.

`/space/slug` is absolute, `slug` and `./slug` resolve inside the page's own
space, and anything with a scheme is somebody else's URL and is left alone.

## Running it

```sh
task charted:up      # site on :8081, Postgres on :5433, write token "dev"
task charted:logs
task charted:down    # also resets the database, which lives in tmpfs
```

That builds the image, which is the whole site. To work on the server itself,
point it at the same database and run it directly:

```sh
task charted:up
DATABASE_URL=postgres://charted:charted@127.0.0.1:5433/charted?sslmode=disable \
  CHARTED_TOKEN=dev go run ./services/charted
```

`task charted:dev` runs the reader in front of whichever of those is listening.

| Variable | What it does |
|---|---|
| `DATABASE_URL` | Required. Postgres connection string. |
| `CHARTED_TOKEN` | The write token. Unset means read-only. |
| `CHARTED_SITE_DIR` | The built reader. Unset means API only. |
| `PORT` | Defaults to 8081. |

## Deploying

`infra/charted/compose.yml`, as its own Coolify
resource. One file per deployed thing: a second web service inside the sync
server's compose file was once enough for Coolify to re-derive the domain and
drop the site off its proxy.
