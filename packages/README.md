# packages

Code more than one program here uses. Nothing in this directory is a program.

```
ops/        The merge: one implementation, run by the desktop app and the sync server.
pgmigrate/  Running a directory of .sql files against Postgres, once each, safely.
site/       Serving a built single-page bundle, with the index as the fallback.
mindmap/    Map layout and canvas rendering. TypeScript.
ui/         Panel dragging and tail-following. TypeScript.
```

## The rule

Something moves here when a second program needs it, and the move is the point:
Go's `internal/` rule means `apps/studio/internal/...` cannot be imported from
outside that app, so sharing is never accidental. It is a directory move that
shows up in a diff and a README, rather than an import somebody added.

The reverse rule matters as much. A package here may not import an app or a
service, and may not know which one is calling: `site` takes the two answers
that differ — how to word a refusal — as function arguments rather than
deciding them, and `pgmigrate` takes the table name and the lock number rather
than picking one.

## Two languages, two ways in

The Go packages are ordinary imports — `dev.jevido/work/packages/ops`.

The TypeScript ones are not npm packages and there is no workspace: `mindmap`
and `ui` are plain source directories, reached through the `@mindmap/*` and
`@ui/*` aliases that each front end declares twice, in its `vite.config.ts` for
the bundler and in its `tsconfig.json` for the typechecker. Both apps that use
them build them from source, which is why neither has a `package.json` — adding
one would mean a build step, a version, and a publish nobody wants.

What is in there is deliberately narrow: geometry, painting, and a pointer with
two numbers. A drift in the map means boxes a few pixels apart in two apps. The
document model is not here and must not come here — two implementations of a
merge mean two people looking at different documents, with no correct side.
