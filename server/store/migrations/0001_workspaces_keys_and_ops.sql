-- A workspace is one board: the unit a key opens and the unit the sequence
-- numbers are counted in.
--
-- head_seq is not a cache of max(ops.seq). It is where the next sequence number
-- comes from, and holding it on this row is what makes the log gapless: an
-- append locks this row, takes the numbers it needs, and writes the rows under
-- the same transaction, so a rollback returns the numbers instead of leaking
-- them the way a Postgres SEQUENCE would.
CREATE TABLE workspaces (
    id         text        PRIMARY KEY,
    name       text        NOT NULL,
    head_seq   bigint      NOT NULL DEFAULT 0 CHECK (head_seq >= 0),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Keys are stored as SHA-256 hashes, so the table is not a list of credentials.
-- The hash is the lookup key as well as the secret's shadow: a caller is
-- identified by hashing what they sent and looking it up, which is a plain
-- index probe with nothing to compare in variable time.
CREATE TABLE workspace_keys (
    key_hash     bytea       PRIMARY KEY,
    workspace_id text        NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    access       text        NOT NULL CHECK (access IN ('read', 'write')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz
);

CREATE INDEX workspace_keys_by_workspace ON workspace_keys (workspace_id);

-- The log. Append-only: there is no UPDATE or DELETE against this table
-- anywhere in the server, and a workspace being dropped is the only thing that
-- removes a row.
CREATE TABLE ops (
    workspace_id text        NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    seq          bigint      NOT NULL CHECK (seq > 0),
    op_id        text        NOT NULL,
    body         jsonb       NOT NULL,
    received_at  timestamptz NOT NULL DEFAULT now(),

    -- Also the index every read uses: fetching the log is a range scan over
    -- (workspace_id, seq > since) in exactly this order.
    PRIMARY KEY (workspace_id, seq)
);

-- Idempotency, enforced by the database rather than by the code that checks
-- first. Two requests carrying the same op can race past any check; one of them
-- loses here instead of writing the op twice.
CREATE UNIQUE INDEX ops_id_per_workspace ON ops (workspace_id, op_id);
