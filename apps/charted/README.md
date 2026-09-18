# Charted — reader

The page half of Charted. A static Svelte bundle that fetches already-rendered
pages and puts them on screen.

Plain Svelte, not SvelteKit, and for once that is not only about keeping the
build small: every page is rendered to HTML by
[`services/charted`](../../services/charted/README.md) when it is written, so
there is nothing here to server-render. What ships is one bundle that reads
JSON.

## What is in it

- A sidebar built from `/v1/nav` — every space and its pages, in one request.
  A documentation site is tens of pages, and a nav that loads per section is a
  nav that flickers on every navigation.
- The page itself from `/v1/pages/{space}/{slug...}`, inserted as HTML. Safe
  because the server renders with raw HTML disabled: what arrives is goldmark's
  output over somebody's markdown, not somebody's markup.
- Search on ⌘K, one debounced request per keystroke pause with the previous one
  aborted, so the results always answer what is in the box.
- An on-page contents list, tracked with an IntersectionObserver rather than a
  scroll handler.
- Light and dark, set on `<html>` before the first paint by a script in
  `index.html` — a theme applied after mount is a theme applied one frame after
  a white page.

Routing is thirty lines in `src/lib/router.ts`. A page is `/<space>/<slug>` and
the slug may contain slashes; that is the whole route table. The server answers
`index.html` for every path that is not a file, which is what makes a deep link
work on a cold load.

## Running it

```sh
task charted:up     # the server and its Postgres, from the repository root
task charted:dev    # this page on :5176, proxying /v1 to it
```

In production this bundle is not served from here at all: the image builds it
and the Charted binary hands it out, so the site is one container.
