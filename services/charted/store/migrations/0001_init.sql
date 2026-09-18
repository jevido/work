-- Charted's whole schema.
--
-- Pages are rows, not files. The alternative -- markdown committed beside the
-- code and rendered at build time -- is the usual shape, and it is the right
-- one when the docs live in the repository they describe. These do not: they
-- are written by an agent in documentation mode, from a machine that is not the
-- one serving them, so the write has to land somewhere both sides can reach.
-- That is a database.
--
-- Rendered HTML is stored beside the markdown on purpose. Rendering is
-- deterministic and slow-ish, a page is read far more often than it is written,
-- and a renderer upgrade that changes output should be a visible re-render of
-- every page rather than a silent difference between two requests.

create table if not exists spaces (
    slug        text primary key,
    title       text        not null,
    summary     text        not null default '',
    -- Hand-ordered, because documentation is read in an order somebody chose.
    -- Ties break on title, so an unset position is still deterministic.
    position    integer     not null default 0,
    created_at  timestamptz not null default now()
);

create table if not exists pages (
    space       text        not null references spaces (slug) on delete cascade,
    -- The path inside the space: "install", "guides/first-run". No leading
    -- slash, no extension. This is what the reader's URL carries.
    slug        text        not null,
    title       text        not null,
    description text        not null default '',
    markdown    text        not null,
    html        text        not null,
    -- The same page with every tag stripped, kept only so search has something
    -- to index and to excerpt from. Never served.
    plain       text        not null,
    -- The headings, as [{"id","text","level"}], for the on-page contents.
    toc         jsonb       not null default '[]'::jsonb,
    position    integer     not null default 0,
    updated_at  timestamptz not null default now(),

    primary key (space, slug)
);

-- Search is Postgres', not a second system. One index over the title, the
-- description and the body, weighted so a title match beats a body mention.
create index if not exists pages_fts on pages using gin (
    (
        setweight(to_tsvector('english', title), 'A') ||
        setweight(to_tsvector('english', description), 'B') ||
        setweight(to_tsvector('english', plain), 'C')
    )
);

create index if not exists pages_space_position on pages (space, position, slug);

-- Every internal link a page makes, written when the page is. The checker is a
-- query over this and pages, which means a broken link is found by the same
-- transaction that could have created it rather than by a crawl afterwards.
create table if not exists links (
    space      text not null,
    slug       text not null,
    to_space   text not null,
    to_slug    text not null,
    -- The link's own text, so a report can say which words to fix.
    label      text not null default '',

    foreign key (space, slug) references pages (space, slug) on delete cascade
);

create index if not exists links_target on links (to_space, to_slug);
create index if not exists links_source on links (space, slug);
