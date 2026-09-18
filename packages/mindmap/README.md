# mindmap

Map layout and canvas rendering, shared by the desktop app and the website.

```
layout.ts     Where the boxes go: a tidy-tree over the document's shape.
renderer.ts   Painting them: canvas 2D, the curves between them, hit testing.
icons.ts      The icon names a card may carry, and their labels.
palette.ts    The colours a region may be, in both themes.
```

Shared rather than copied because the alternative was six hundred lines of
tidy-tree and bezier maths living in two apps, which drifts in exactly the same
way and is harder to notice. Shared rather than merged into one app because the
website has no document model at all: it is handed a merged tree and draws it.

What that buys is the limit on what may come here. This is geometry and
painting, with no document model in it — a drift here means the boxes are a few
pixels apart in two apps, not that two people are looking at different
documents. Anything that decides *what* a card is stays in the app.

Not an npm package and not a workspace member: it is plain source, reached
through the `@mindmap/*` alias that each front end declares twice, in
`vite.config.ts` for the bundler and in `tsconfig.json` for the typechecker.
Both apps build it from source.
