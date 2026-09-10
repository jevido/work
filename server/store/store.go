// Package store is the sync server's Postgres: workspaces, the keys that open
// them, and the op log.
//
// It stores ops and hands them back in order. It does not merge them and it
// does not know what one means — that is dev.jevido/work/internal/ops, which
// both this server and the desktop app run. The only thing this package knows
// about an op is that it has an ID, because the ID is what makes an append
// idempotent.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"dev.jevido/work/internal/ops"
)

// Errors a caller is expected to handle rather than pass on as a 500.
var (
	// ErrNoKey means the key is unknown, malformed or revoked. The three are
	// deliberately one error: telling a caller which of them it was tells them
	// whether a key they are guessing at exists.
	ErrNoKey = errors.New("no such key")
	// ErrNoWorkspace means the workspace is gone. A key outliving its workspace
	// should be impossible — the foreign key cascades — so this is corruption,
	// not a normal outcome.
	ErrNoWorkspace = errors.New("no such workspace")
	// ErrOpConflict means an op ID already in the log arrived again carrying
	// different content. That is a client generating IDs wrongly, not a retry.
	ErrOpConflict = errors.New("op id already used for different content")
)

// Access is what a key may do.
type Access string

const (
	// AccessRead may read the log and the workspace.
	AccessRead Access = "read"
	// AccessWrite may do that and append.
	AccessWrite Access = "write"
)

// Allows reports whether this access covers what a request needs. Write covers
// read, so the two constants are a rank rather than a set.
func (a Access) Allows(needed Access) bool {
	return a == AccessWrite || a == needed
}

// Workspace is one board.
type Workspace struct {
	ID   string
	Name string
	// Head is the highest sequence number in the log. A fresh workspace is 0.
	Head      int64
	CreatedAt time.Time
}

// Auth is what a key turned out to be.
type Auth struct {
	WorkspaceID string
	Access      Access
}

// Created is a new workspace and the only time its keys are readable.
type Created struct {
	Workspace Workspace
	WriteKey  string
	ReadKey   string
}

// Entry is one op as the log holds it: the op itself, plus where it landed.
type Entry struct {
	Seq        int64
	ReceivedAt time.Time
	Op         ops.Op
}

// Appended is where one op ended up. It is returned for ops written by this
// request and for ops that were already there, because a client that lost the
// response to an earlier attempt needs the same answer either way.
type Appended struct {
	OpID string
	Seq  int64
}

// AppendResult is the outcome of an append.
type AppendResult struct {
	// Accepted are the ops this request wrote.
	Accepted []Appended
	// Duplicates are the ops that were already in the log, unchanged.
	Duplicates []Appended
	// Head is the workspace's highest sequence number afterwards.
	Head int64
}

// Store is the server's Postgres.
type Store struct {
	pool *pgxpool.Pool
}

// New wraps an open pool. The pool is the caller's to close.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Ping checks the database is reachable, for the health endpoint. It is a real
// round-trip: a health check that only proves the process is running is a
// health check that stays green through an outage.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Lookup resolves a key to the workspace it opens.
//
// The key is hashed and looked up by hash, so the query is an index probe on a
// value an attacker cannot steer, and nothing here compares a secret.
func (s *Store) Lookup(ctx context.Context, key string) (Auth, error) {
	if !plausibleKey(key) {
		return Auth{}, ErrNoKey
	}
	sum := sha256.Sum256([]byte(key))

	var auth Auth
	err := s.pool.QueryRow(ctx,
		`SELECT workspace_id, access
		   FROM workspace_keys
		  WHERE key_hash = $1 AND revoked_at IS NULL`,
		sum[:],
	).Scan(&auth.WorkspaceID, &auth.Access)
	if errors.Is(err, pgx.ErrNoRows) {
		return Auth{}, ErrNoKey
	}
	if err != nil {
		return Auth{}, fmt.Errorf("looking up key: %w", err)
	}
	return auth, nil
}

// Workspace reads one workspace, including its current head.
func (s *Store) Workspace(ctx context.Context, id string) (Workspace, error) {
	var found Workspace
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, head_seq, created_at FROM workspaces WHERE id = $1`, id,
	).Scan(&found.ID, &found.Name, &found.Head, &found.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, ErrNoWorkspace
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("reading workspace %s: %w", id, err)
	}
	return found, nil
}

// CreateWorkspace opens a workspace and mints its two keys.
//
// The plaintext keys are returned once and never stored, so this is the only
// moment they exist anywhere but in the caller's hands.
func (s *Store) CreateWorkspace(ctx context.Context, name string) (Created, error) {
	writeKey, writeHash, err := newKey("wk_")
	if err != nil {
		return Created{}, err
	}
	readKey, readHash, err := newKey("rk_")
	if err != nil {
		return Created{}, err
	}
	id, err := token("ws_")
	if err != nil {
		return Created{}, err
	}

	created := Created{WriteKey: writeKey, ReadKey: readKey}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`INSERT INTO workspaces (id, name) VALUES ($1, $2)
			 RETURNING id, name, head_seq, created_at`,
			id, name)
		if err := row.Scan(&created.Workspace.ID, &created.Workspace.Name,
			&created.Workspace.Head, &created.Workspace.CreatedAt); err != nil {
			return fmt.Errorf("inserting workspace: %w", err)
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO workspace_keys (key_hash, workspace_id, access)
			 VALUES ($1, $3, 'write'), ($2, $3, 'read')`,
			writeHash, readHash, id)
		if err != nil {
			return fmt.Errorf("inserting keys: %w", err)
		}
		return nil
	})
	if err != nil {
		return Created{}, err
	}
	return created, nil
}

// Append writes ops to the end of a workspace's log.
//
// Either every op in the call lands or none does, and the sequence numbers it
// assigns are contiguous with what was already there. Ops whose IDs are already
// in the log are reported as duplicates with the sequence numbers they already
// had, which is what makes retrying a request whose response was lost safe: the
// second attempt writes nothing and answers the same as the first.
func (s *Store) Append(ctx context.Context, workspaceID string, list []ops.Op) (AppendResult, error) {
	var result AppendResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Lock the workspace row first, before anything is read.
		//
		// This is the whole concurrency design. Everything that appends to a
		// workspace queues here, so the duplicate scan below sees committed
		// reality rather than a snapshot another append is about to invalidate.
		// Doing it the other way round — scan, then lock — lets two requests
		// carrying the same op both decide it is new. Serialising per workspace
		// costs nothing worth having: a workspace is one person's board.
		var head int64
		err := tx.QueryRow(ctx,
			`SELECT head_seq FROM workspaces WHERE id = $1 FOR UPDATE`, workspaceID,
		).Scan(&head)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoWorkspace
		}
		if err != nil {
			return fmt.Errorf("locking workspace %s: %w", workspaceID, err)
		}

		known, err := existing(ctx, tx, workspaceID, list)
		if err != nil {
			return err
		}

		fresh := make([]ops.Op, 0, len(list))
		seen := make(map[string]bool, len(list))
		for _, op := range list {
			// A request that repeats an op ID within itself is the same client
			// bug as one that repeats it across requests, and is caught here
			// rather than by the unique index, so the error names the op.
			if seen[op.ID] {
				return fmt.Errorf("%w: %s appears twice in one request", ErrOpConflict, op.ID)
			}
			seen[op.ID] = true

			if prior, ok := known[op.ID]; ok {
				if !prior.same(op) {
					return fmt.Errorf("%w: %s", ErrOpConflict, op.ID)
				}
				result.Duplicates = append(result.Duplicates, Appended{OpID: op.ID, Seq: prior.seq})
				continue
			}
			fresh = append(fresh, op)
		}

		if len(fresh) > 0 {
			head, err = insert(ctx, tx, workspaceID, head, fresh)
			if err != nil {
				return err
			}
			result.Accepted = make([]Appended, len(fresh))
			for i, op := range fresh {
				result.Accepted[i] = Appended{OpID: op.ID, Seq: head - int64(len(fresh)-1-i)}
			}
		}
		result.Head = head
		return nil
	})
	if err != nil {
		return AppendResult{}, err
	}
	return result, nil
}

// priorOp is an op already in the log, kept only long enough to decide whether
// an arriving op with the same ID is a retry or a collision.
type priorOp struct {
	seq  int64
	body []byte
}

// same reports whether an arriving op is the one already stored.
//
// Both sides are re-encoded rather than compared as bytes: the stored copy has
// been through jsonb, which reorders object keys and drops whitespace, so the
// bytes never match even for a byte-identical retry. Decoding to an Op and
// marshalling again puts both through the same canonical form.
func (p priorOp) same(arriving ops.Op) bool {
	var stored ops.Op
	if err := json.Unmarshal(p.body, &stored); err != nil {
		return false
	}
	storedJSON, err := json.Marshal(stored)
	if err != nil {
		return false
	}
	arrivingJSON, err := json.Marshal(arriving)
	if err != nil {
		return false
	}
	return string(storedJSON) == string(arrivingJSON)
}

// existing finds which of these op IDs the log already holds.
func existing(ctx context.Context, tx pgx.Tx, workspaceID string, list []ops.Op) (map[string]priorOp, error) {
	ids := make([]string, len(list))
	for i, op := range list {
		ids[i] = op.ID
	}

	rows, err := tx.Query(ctx,
		`SELECT op_id, seq, body FROM ops WHERE workspace_id = $1 AND op_id = ANY($2)`,
		workspaceID, ids)
	if err != nil {
		return nil, fmt.Errorf("checking for known ops: %w", err)
	}
	defer rows.Close()

	known := make(map[string]priorOp)
	for rows.Next() {
		var id string
		var prior priorOp
		if err := rows.Scan(&id, &prior.seq, &prior.body); err != nil {
			return nil, fmt.Errorf("scanning known op: %w", err)
		}
		known[id] = prior
	}
	return known, rows.Err()
}

// insert writes the ops and moves the head, returning the new head.
//
// The head is moved by one UPDATE rather than by counting rows afterwards, and
// under the row lock taken at the top of the transaction, so the numbers this
// hands out are contiguous and are given back if the transaction rolls back.
// A Postgres SEQUENCE would be simpler and would leave holes.
func insert(ctx context.Context, tx pgx.Tx, workspaceID string, head int64, fresh []ops.Op) (int64, error) {
	var newHead int64
	err := tx.QueryRow(ctx,
		`UPDATE workspaces SET head_seq = head_seq + $2 WHERE id = $1 RETURNING head_seq`,
		workspaceID, int64(len(fresh)),
	).Scan(&newHead)
	if err != nil {
		return 0, fmt.Errorf("advancing the sequence: %w", err)
	}

	batch := &pgx.Batch{}
	for i, op := range fresh {
		body, err := json.Marshal(op)
		if err != nil {
			return 0, fmt.Errorf("encoding op %s: %w", op.ID, err)
		}
		batch.Queue(
			`INSERT INTO ops (workspace_id, seq, op_id, body) VALUES ($1, $2, $3, $4::jsonb)`,
			workspaceID, head+int64(i)+1, op.ID, string(body))
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		// Two requests carrying the same new op can both get past the scan
		// above only if one of them started before the other committed; the
		// unique index is the backstop, and it reads as a conflict rather than
		// as a server fault.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return 0, fmt.Errorf("%w: %s", ErrOpConflict, pgErr.Detail)
		}
		return 0, fmt.Errorf("inserting ops: %w", err)
	}
	return newHead, nil
}

// Log returns ops with a sequence number above since, in order, and the
// workspace's head.
//
// The head is read after the rows, never before. Read first, it could miss an
// op committed while the rows were being fetched and report a client as caught
// up when it is not; read after, the worst it can do is say there is more when
// the client already has it, and the client asks again and gets nothing.
func (s *Store) Log(ctx context.Context, workspaceID string, since int64, limit int) ([]Entry, int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT seq, received_at, body
		   FROM ops
		  WHERE workspace_id = $1 AND seq > $2
		  ORDER BY seq
		  LIMIT $3`,
		workspaceID, since, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("reading the log: %w", err)
	}
	defer rows.Close()

	var log []Entry
	for rows.Next() {
		var entry Entry
		var body []byte
		if err := rows.Scan(&entry.Seq, &entry.ReceivedAt, &body); err != nil {
			return nil, 0, fmt.Errorf("scanning op: %w", err)
		}
		if err := json.Unmarshal(body, &entry.Op); err != nil {
			return nil, 0, fmt.Errorf("decoding op at seq %d: %w", entry.Seq, err)
		}
		log = append(log, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("reading the log: %w", err)
	}
	rows.Close()

	workspace, err := s.Workspace(ctx, workspaceID)
	if err != nil {
		return nil, 0, err
	}
	return log, workspace.Head, nil
}

// tokenBytes is how much randomness an identifier or a key carries. Sixteen
// bytes is 128 bits, which is not guessable and still fits on one line of a
// terminal.
const tokenBytes = 16

// token mints a prefixed random string.
//
// Workspace IDs and keys are the same shape because they want the same thing:
// something opaque, unguessable and safe in a URL. An ID that sorted by time
// would be prettier in a listing and would buy nothing — the table it keys
// holds one row per board.
func token(prefix string) (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating %s token: %w", prefix, err)
	}
	return prefix + hex.EncodeToString(raw), nil
}

// newKey mints a key and the hash the database keeps instead of it.
func newKey(prefix string) (key string, hash []byte, err error) {
	key, err = token(prefix)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256([]byte(key))
	return key, sum[:], nil
}

// plausibleKey rejects what cannot be a key before it reaches the database, so
// a flood of nonsense is not a flood of queries.
func plausibleKey(key string) bool {
	if len(key) != len("wk_")+2*tokenBytes {
		return false
	}
	return strings.HasPrefix(key, "wk_") || strings.HasPrefix(key, "rk_")
}
