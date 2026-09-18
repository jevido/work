# site

Serves a built single-page bundle: the files a bundler emitted, with every
unknown path falling back to `index.html`.

Both servers here do exactly this — sync hands out `apps/website`, Charted hands
out `apps/charted` — and the second was a second implementation of the first,
with only one of them tested.

Written by hand rather than with `http.FileServer`, which is close and wrong in
three ways for a bundle like this: it redirects `/index.html` to `/`, it lists a
directory that has no index in it, and it answers 404 for a path the client
router owns. All three would have to be undone.

Two things the caller keeps, because they are the parts that genuinely differ:

```go
site.Handler(fsys, site.Options{
    OnBroken: func(w http.ResponseWriter, r *http.Request, err error) { /* your 500 */ },
    OnMethod: func(w http.ResponseWriter, r *http.Request) { /* your 405 */ },
})
```

Both servers answer JSON, in their own shape. `OnBroken` exists because an
unreadable `index.html` is the server's own fault rather than the request's, and
it has to be able to become that 500 — which it cannot once a `Cache-Control`
for a page that will never be sent is already on the response. Both hooks have
plain-text defaults, so a caller that does not care still gets something sane.

The cache split is possible only because the bundler hashes what it emits into
`assets/`: a hashed name can never mean two different things, so it is
`immutable`, and the index that names it may not be held at all or a deploy
would be invisible to anyone who had already visited.

The fallback answers 200, not 404 with a body. A browser reloading a route it
was already on should get the same page it had.
