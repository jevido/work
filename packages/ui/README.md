# ui

Two pieces of interaction behaviour, shared by the desktop app and the website.

```
drag.svelte.ts   Dragging a floating panel by its header, and keeping it on screen.
follow.ts        Following the tail of a scrolling list, and letting go when you scroll up.
```

The same trade as [`mindmap`](../mindmap/README.md), one level smaller: both are
a pointer and a couple of numbers, with no document model in them. A drift here
means a panel that clamps to its edges slightly differently in two apps.

`drag.svelte.ts` carries the `.svelte.ts` extension because it uses runes, so
the Svelte compiler has to see it. That is also why this cannot become an
ordinary npm package without a build step nobody wants: it is source both apps
compile.

Reached through the `@ui/*` alias, declared in each front end's
`vite.config.ts` and `tsconfig.json`.
