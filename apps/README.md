# apps

Programs a person opens and looks at. One directory each.

```
studio/     The desktop workbench. Wails: Go and Svelte compiled into one binary.
website/    work.jevido.app — the read-only viewer somebody is sent a link to.
charted/    The documentation site's reader.
```

Two of the three are a page and nothing else — `index.html`, `src/`, a vite
config, a `Taskfile.yml` — because the server that feeds them is a separate
program under [`services`](../services/README.md). That is the split the whole
repository is arranged around: what a person opens lives here, what runs with
nobody looking at it lives there.

The desktop app is the exception and cannot be split. A Wails binary is Go and a
front end compiled into one file, and that file is the thing somebody opens, so
its Go lives beside its Svelte. It is the only directory here with a `main.go`
in it.

What two of them share is not copied between them: `@mindmap/*` and `@ui/*`
resolve into [`packages`](../packages/README.md) through an alias declared in
each app's `vite.config.ts` and `tsconfig.json`.

Each app has its own README. None of them is built from here —
`task studio:dev`, `task website:dev`, `task charted:dev` from the repository
root, or `task` for the whole list.
