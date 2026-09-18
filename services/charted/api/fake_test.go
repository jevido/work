package api

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"dev.jevido/work/services/charted/render"
	"dev.jevido/work/services/charted/store"
)

// fakeStore is the whole database, in memory. Every handler here is argument
// checking and shape, so what it is talking to has to be predictable rather
// than realistic -- the queries themselves are the store's own business and
// need a Postgres to say anything about.
type fakeStore struct {
	pages  map[string]store.Page
	spaces []store.NavSpace
	hits   []store.Hit
	broken []store.Broken

	// What the last write was handed, so a test can assert the path was parsed
	// rather than assert on a round trip.
	lastWrite store.PageInput
	lastSpace store.Space
	deleted   []string

	// Faults, so the error paths are reachable.
	pingErr   error
	navErr    error
	writeErr  error
	writeSaid []render.Link
}

func newFake() *fakeStore {
	return &fakeStore{pages: map[string]store.Page{}}
}

func key(space, slug string) string { return space + "/" + slug }

func (f *fakeStore) Ping(context.Context) error { return f.pingErr }

func (f *fakeStore) Nav(context.Context) ([]store.NavSpace, error) {
	return f.spaces, f.navErr
}

func (f *fakeStore) Page(_ context.Context, space, slug string) (store.Page, error) {
	page, ok := f.pages[key(space, slug)]
	if !ok {
		return store.Page{}, store.ErrNotFound
	}
	return page, nil
}

func (f *fakeStore) Search(_ context.Context, _ string, _ int) ([]store.Hit, error) {
	return f.hits, nil
}

func (f *fakeStore) UpsertPage(_ context.Context, in store.PageInput) ([]render.Link, error) {
	f.lastWrite = in
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	f.pages[key(in.Space, in.Slug)] = store.Page{
		Space: in.Space, Slug: in.Slug, Title: in.Title, Markdown: in.Markdown,
	}
	return f.writeSaid, nil
}

func (f *fakeStore) DeletePage(_ context.Context, space, slug string) error {
	if _, ok := f.pages[key(space, slug)]; !ok {
		return store.ErrNotFound
	}
	delete(f.pages, key(space, slug))
	f.deleted = append(f.deleted, key(space, slug))
	return nil
}

func (f *fakeStore) UpsertSpace(_ context.Context, in store.Space) error {
	f.lastSpace = in
	return nil
}

func (f *fakeStore) BrokenLinks(context.Context) ([]store.Broken, error) {
	return f.broken, nil
}

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// errDown stands in for anything the database can fail with. Its text is
// distinctive so a test can assert it did *not* reach the caller.
var errDown = errors.New("pgx: connection refused to charted_pages")
