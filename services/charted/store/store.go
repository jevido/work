// Package store is Charted's database: spaces, pages, search and the link
// graph.
//
// Everything a page needs to be served is one row. Nav is one query, a page is
// one query, search is one query -- there is no join fan-out here and no cache,
// because the read volume of a documentation site is small and the failure mode
// of a cache is serving what was true an hour ago.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"dev.jevido/work/services/charted/render"
)

// ErrNotFound is what a read returns for a page or space that is not there. The
// API turns it into a 404 and nothing else looks at it.
var ErrNotFound = errors.New("charted: not found")

type Store struct {
	pool *pgxpool.Pool
}

// Open connects and verifies the connection before returning.
//
// Verified rather than lazy: a bad DSN that only fails on the first request is
// a deploy that looks healthy until somebody reads a page.
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("charted: parsing DATABASE_URL: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("charted: connecting: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("charted: reaching the database: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

// Ping is the health check's whole implementation.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

/* -------------------------------------------------------------------------- */
/* Writing                                                                    */
/* -------------------------------------------------------------------------- */

// Space is a top-level section of the site: one product, one area.
type Space struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	Position int    `json:"position"`
}

// PageInput is a page as it is written. The rendered forms are derived here, so
// a caller cannot store HTML that does not match the markdown beside it.
type PageInput struct {
	Space       string `json:"space"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Markdown    string `json:"markdown"`
	Position    int    `json:"position"`
}

// UpsertSpace creates a space or updates the parts of it that are not pages.
func (s *Store) UpsertSpace(ctx context.Context, in Space) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO spaces (slug, title, summary, position)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (slug) DO UPDATE
		SET title = excluded.title,
		    summary = excluded.summary,
		    position = excluded.position`,
		in.Slug, in.Title, in.Summary, in.Position)
	if err != nil {
		return fmt.Errorf("charted: writing space %s: %w", in.Slug, err)
	}
	return nil
}

// UpsertPage renders and stores one page, and replaces its links.
//
// It returns the links on this page that point at pages which do not exist.
// That is the whole of link checking, and it happens here rather than in a
// crawl afterwards for one reason: the transaction that could create a broken
// link is the one that can tell you about it, at a moment when the person who
// wrote it is still there.
//
// A broken link is reported, not refused. Documentation is written in an order
// -- the overview before the page it points at -- and a writer who cannot save
// the first page until the second exists writes the second page badly.
func (s *Store) UpsertPage(ctx context.Context, in PageInput) ([]render.Link, error) {
	page, err := render.Render(in.Space, in.Markdown)
	if err != nil {
		return nil, fmt.Errorf("charted: rendering %s/%s: %w", in.Space, in.Slug, err)
	}

	toc, err := json.Marshal(page.Headings)
	if err != nil {
		return nil, fmt.Errorf("charted: encoding contents: %w", err)
	}

	var broken []render.Link
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// The space has to exist for the foreign key, and a page written into
		// a space nobody named is a page nobody can navigate to. Created with
		// the slug as its title rather than refused -- the title is one PUT
		// away and the page is not lost in the meantime.
		if _, err := tx.Exec(ctx, `
			INSERT INTO spaces (slug, title) VALUES ($1, $1)
			ON CONFLICT (slug) DO NOTHING`, in.Space); err != nil {
			return fmt.Errorf("ensuring space %s: %w", in.Space, err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO pages (space, slug, title, description, markdown, html, plain, toc, position, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
			ON CONFLICT (space, slug) DO UPDATE
			SET title = excluded.title,
			    description = excluded.description,
			    markdown = excluded.markdown,
			    html = excluded.html,
			    plain = excluded.plain,
			    toc = excluded.toc,
			    position = excluded.position,
			    updated_at = now()`,
			in.Space, in.Slug, in.Title, in.Description,
			in.Markdown, page.HTML, page.Plain, toc, in.Position); err != nil {
			return fmt.Errorf("writing page: %w", err)
		}

		// Replaced wholesale. A link that was removed from the markdown has to
		// stop being a link, and working out which rows to delete one by one is
		// the same work with more ways to be wrong.
		if _, err := tx.Exec(ctx,
			`DELETE FROM links WHERE space = $1 AND slug = $2`, in.Space, in.Slug); err != nil {
			return fmt.Errorf("clearing links: %w", err)
		}
		for _, link := range page.Links {
			if _, err := tx.Exec(ctx, `
				INSERT INTO links (space, slug, to_space, to_slug, label)
				VALUES ($1, $2, $3, $4, $5)`,
				in.Space, in.Slug, link.Space, link.Slug, link.Label); err != nil {
				return fmt.Errorf("writing link: %w", err)
			}
		}

		rows, err := tx.Query(ctx, `
			SELECT l.to_space, l.to_slug, l.label
			FROM links l
			LEFT JOIN pages p ON p.space = l.to_space AND p.slug = l.to_slug
			WHERE l.space = $1 AND l.slug = $2 AND p.slug IS NULL`,
			in.Space, in.Slug)
		if err != nil {
			return fmt.Errorf("checking links: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var link render.Link
			if err := rows.Scan(&link.Space, &link.Slug, &link.Label); err != nil {
				return fmt.Errorf("reading a broken link: %w", err)
			}
			broken = append(broken, link)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("charted: %w", err)
	}
	return broken, nil
}

// DeletePage removes a page. Its links go with it through the foreign key;
// links *to* it stay, and start reporting as broken, which is the point.
func (s *Store) DeletePage(ctx context.Context, space, slug string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM pages WHERE space = $1 AND slug = $2`, space, slug)
	if err != nil {
		return fmt.Errorf("charted: deleting %s/%s: %w", space, slug, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

/* -------------------------------------------------------------------------- */
/* Reading                                                                    */
/* -------------------------------------------------------------------------- */

// Page is one page, as it is served.
type Page struct {
	Space       string           `json:"space"`
	Slug        string           `json:"slug"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	HTML        string           `json:"html"`
	Markdown    string           `json:"markdown"`
	TOC         []render.Heading `json:"toc"`
	UpdatedAt   time.Time        `json:"updatedAt"`
}

// Page reads one page. markdown is included: the reader does not use it, but
// the writer does -- documentation mode reads a page back before it edits it,
// and a round trip through rendered HTML is not one.
func (s *Store) Page(ctx context.Context, space, slug string) (Page, error) {
	var p Page
	var toc []byte
	err := s.pool.QueryRow(ctx, `
		SELECT space, slug, title, description, html, markdown, toc, updated_at
		FROM pages WHERE space = $1 AND slug = $2`, space, slug).
		Scan(&p.Space, &p.Slug, &p.Title, &p.Description, &p.HTML, &p.Markdown, &toc, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Page{}, ErrNotFound
	}
	if err != nil {
		return Page{}, fmt.Errorf("charted: reading %s/%s: %w", space, slug, err)
	}
	if err := json.Unmarshal(toc, &p.TOC); err != nil {
		return Page{}, fmt.Errorf("charted: reading contents of %s/%s: %w", space, slug, err)
	}
	return p, nil
}

// NavPage is one line of the sidebar.
type NavPage struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// NavSpace is one section of the sidebar and its pages, in order.
type NavSpace struct {
	Slug    string    `json:"slug"`
	Title   string    `json:"title"`
	Summary string    `json:"summary"`
	Pages   []NavPage `json:"pages"`
}

// Nav is the whole sidebar in one query.
//
// The whole thing, not a level at a time: a documentation site is tens of
// pages, the rows are three short strings each, and a nav that loads per
// section is a nav that flickers on every navigation.
func (s *Store) Nav(ctx context.Context) ([]NavSpace, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sp.slug, sp.title, sp.summary,
		       COALESCE(p.slug, ''), COALESCE(p.title, ''), COALESCE(p.description, '')
		FROM spaces sp
		LEFT JOIN pages p ON p.space = sp.slug
		ORDER BY sp.position, sp.title, p.position, p.slug`)
	if err != nil {
		return nil, fmt.Errorf("charted: reading the nav: %w", err)
	}
	defer rows.Close()

	var out []NavSpace
	for rows.Next() {
		var space NavSpace
		var page NavPage
		if err := rows.Scan(&space.Slug, &space.Title, &space.Summary,
			&page.Slug, &page.Title, &page.Description); err != nil {
			return nil, fmt.Errorf("charted: reading a nav row: %w", err)
		}
		if len(out) == 0 || out[len(out)-1].Slug != space.Slug {
			space.Pages = []NavPage{}
			out = append(out, space)
		}
		// An empty space arrives as one row with no page, through the LEFT
		// JOIN. It keeps its heading: a section with nothing under it says
		// "nothing written here yet", which is worth knowing.
		if page.Slug != "" {
			at := len(out) - 1
			out[at].Pages = append(out[at].Pages, page)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("charted: reading the nav: %w", err)
	}
	return out, nil
}

// Hit is one search result.
type Hit struct {
	Space   string  `json:"space"`
	Slug    string  `json:"slug"`
	Title   string  `json:"title"`
	Excerpt string  `json:"excerpt"`
	Rank    float32 `json:"rank"`
}

// Search runs one full-text query over every page.
//
// websearch_to_tsquery rather than plainto_tsquery, because people type
// quoted phrases and -exclusions into search boxes and the former understands
// both instead of matching them literally.
func (s *Store) Search(ctx context.Context, query string, limit int) ([]Hit, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		WITH q AS (SELECT websearch_to_tsquery('english', $1) AS tsq)
		SELECT space, slug, title,
		       ts_headline('english', plain, q.tsq,
		           'MaxWords=28, MinWords=12, ShortWord=2, HighlightAll=false, MaxFragments=1'),
		       ts_rank(
		           setweight(to_tsvector('english', title), 'A') ||
		           setweight(to_tsvector('english', description), 'B') ||
		           setweight(to_tsvector('english', plain), 'C'),
		           q.tsq)
		FROM pages, q
		WHERE (
		    setweight(to_tsvector('english', title), 'A') ||
		    setweight(to_tsvector('english', description), 'B') ||
		    setweight(to_tsvector('english', plain), 'C')
		) @@ q.tsq
		ORDER BY 5 DESC, title
		LIMIT $2`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("charted: searching: %w", err)
	}
	defer rows.Close()

	hits := []Hit{}
	for rows.Next() {
		var hit Hit
		if err := rows.Scan(&hit.Space, &hit.Slug, &hit.Title, &hit.Excerpt, &hit.Rank); err != nil {
			return nil, fmt.Errorf("charted: reading a result: %w", err)
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

// Broken is one link that points at a page which does not exist.
type Broken struct {
	Space   string `json:"space"`
	Slug    string `json:"slug"`
	ToSpace string `json:"toSpace"`
	ToSlug  string `json:"toSlug"`
	Label   string `json:"label"`
}

// BrokenLinks is the whole site's link check, in one query.
func (s *Store) BrokenLinks(ctx context.Context) ([]Broken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT l.space, l.slug, l.to_space, l.to_slug, l.label
		FROM links l
		LEFT JOIN pages p ON p.space = l.to_space AND p.slug = l.to_slug
		WHERE p.slug IS NULL
		ORDER BY l.space, l.slug, l.to_space, l.to_slug`)
	if err != nil {
		return nil, fmt.Errorf("charted: checking links: %w", err)
	}
	defer rows.Close()

	out := []Broken{}
	for rows.Next() {
		var b Broken
		if err := rows.Scan(&b.Space, &b.Slug, &b.ToSpace, &b.ToSlug, &b.Label); err != nil {
			return nil, fmt.Errorf("charted: reading a broken link: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
