package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"dev.jevido/work/packages/ops"
	"dev.jevido/work/services/sync/internal/pgtest"
)

// These tests need a real Postgres, because what they check — a row lock making
// a sequence gapless, a unique index catching a racing insert, jsonb round-
// tripping an op — is exactly the part no fake reproduces. They skip when there
// is no database to talk to:
//
//	TEST_DATABASE_URL=postgres://localhost/work_test?sslmode=disable go test ./...
//
// CI sets WORK_REQUIRE_POSTGRES as well, which turns that skip into a failure;
// see [pgtest].
var shared *pgxpool.Pool

func TestMain(m *testing.M) {
	url := pgtest.URL()
	if url == "" {
		if pgtest.Required() {
			fmt.Fprintf(os.Stderr, "store: %s, but %s is set\n", pgtest.Reason, pgtest.RequireVar)
			os.Exit(1)
		}
		// Not a failure. A contributor without a database still gets the rest
		// of the suite.
		fmt.Fprintln(os.Stderr, "store: "+pgtest.Reason)
		os.Exit(m.Run())
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store: connecting to TEST_DATABASE_URL: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := Migrate(ctx, New(pool), quiet); err != nil {
		fmt.Fprintf(os.Stderr, "store: migrating: %v\n", err)
		os.Exit(1)
	}
	shared = pool
	os.Exit(m.Run())
}

// open returns a store, skipping the test when there is no database.
func open(t *testing.T) *Store {
	t.Helper()
	if shared == nil {
		pgtest.Unavailable(t)
	}
	return New(shared)
}

// aWorkspace creates a workspace for one test to have to itself. Workspaces are
// already the unit everything here is scoped by, so this is all the isolation
// these tests need.
func aWorkspace(t *testing.T, s *Store) Created {
	t.Helper()
	created, err := s.CreateWorkspace(t.Context(), t.Name())
	if err != nil {
		t.Fatalf("creating a workspace: %v", err)
	}
	return created
}

func anOp(id string, clock uint64) ops.Op {
	return ops.Op{
		ID: id, Kind: ops.KindSetFields, Actor: "test", Clock: clock, Node: "n",
		Fields: map[string]json.RawMessage{"title": json.RawMessage(`"x"`)},
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := open(t)
	// TestMain already migrated. Running it again has to be a no-op, because it
	// runs on every start of every instance.
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	for range 3 {
		if err := Migrate(t.Context(), s, quiet); err != nil {
			t.Fatalf("re-running migrations: %v", err)
		}
	}
}

func TestKeysOpenOneWorkspace(t *testing.T) {
	s := open(t)
	created := aWorkspace(t, s)

	for _, tt := range []struct {
		key  string
		want Access
	}{
		{created.WriteKey, AccessWrite},
		{created.ReadKey, AccessRead},
	} {
		auth, err := s.Lookup(t.Context(), tt.key)
		if err != nil {
			t.Fatalf("looking up a key: %v", err)
		}
		if auth.WorkspaceID != created.Workspace.ID {
			t.Errorf("key opens %s, want %s", auth.WorkspaceID, created.Workspace.ID)
		}
		if auth.Access != tt.want {
			t.Errorf("access = %s, want %s", auth.Access, tt.want)
		}
	}

	t.Run("keys are not stored in the clear", func(t *testing.T) {
		var found int
		err := shared.QueryRow(t.Context(),
			`SELECT count(*) FROM workspace_keys WHERE encode(key_hash, 'escape') LIKE 'wk!_%' ESCAPE '!'`,
		).Scan(&found)
		if err != nil {
			t.Fatalf("checking the key table: %v", err)
		}
		if found != 0 {
			t.Errorf("%d keys are stored as their plaintext", found)
		}
	})

	t.Run("a key nobody issued", func(t *testing.T) {
		for _, key := range []string{"", "nonsense", "wk_tooshort", "wk_" + string(make([]byte, 32))} {
			if _, err := s.Lookup(t.Context(), key); !errors.Is(err, ErrNoKey) {
				t.Errorf("Lookup(%q) = %v, want ErrNoKey", key, err)
			}
		}
	})

	t.Run("a revoked key stops working", func(t *testing.T) {
		if _, err := shared.Exec(t.Context(),
			`UPDATE workspace_keys SET revoked_at = now() WHERE workspace_id = $1 AND access = 'read'`,
			created.Workspace.ID); err != nil {
			t.Fatalf("revoking: %v", err)
		}
		if _, err := s.Lookup(t.Context(), created.ReadKey); !errors.Is(err, ErrNoKey) {
			t.Errorf("a revoked key = %v, want ErrNoKey", err)
		}
		if _, err := s.Lookup(t.Context(), created.WriteKey); err != nil {
			t.Errorf("revoking one key broke the other: %v", err)
		}
	})
}

func TestAppendNumbersOpsWithoutGaps(t *testing.T) {
	s := open(t)
	created := aWorkspace(t, s)
	id := created.Workspace.ID

	first, err := s.Append(t.Context(), id, []ops.Op{anOp("a1", 1), anOp("a2", 2)})
	if err != nil {
		t.Fatalf("appending: %v", err)
	}
	if first.Head != 2 {
		t.Errorf("head = %d, want 2", first.Head)
	}
	if got := seqs(first.Accepted); !slices.Equal(got, []int64{1, 2}) {
		t.Errorf("seqs = %v, want [1 2]", got)
	}

	// A second call continues where the first stopped, rather than restarting
	// or leaving a hole.
	second, err := s.Append(t.Context(), id, []ops.Op{anOp("a3", 3)})
	if err != nil {
		t.Fatalf("appending: %v", err)
	}
	if got := seqs(second.Accepted); !slices.Equal(got, []int64{3}) {
		t.Errorf("seqs = %v, want [3]", got)
	}
	if second.Head != 3 {
		t.Errorf("head = %d, want 3", second.Head)
	}
}

func seqs(list []Appended) []int64 {
	out := make([]int64, len(list))
	for i, one := range list {
		out[i] = one.Seq
	}
	return out
}

func TestAppendIsIdempotent(t *testing.T) {
	s := open(t)
	id := aWorkspace(t, s).Workspace.ID
	batch := []ops.Op{anOp("i1", 1), anOp("i2", 2)}

	first, err := s.Append(t.Context(), id, batch)
	if err != nil {
		t.Fatalf("appending: %v", err)
	}

	// The case a client actually hits: it sent the batch, the response never
	// came back, and it has no way to tell that from a failure.
	replay, err := s.Append(t.Context(), id, batch)
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if replay.Head != first.Head {
		t.Errorf("head moved from %d to %d on a replay", first.Head, replay.Head)
	}
	if len(replay.Accepted) != 0 {
		t.Errorf("accepted = %v on a replay, want nothing written", replay.Accepted)
	}
	if !slices.Equal(seqs(replay.Duplicates), seqs(first.Accepted)) {
		t.Errorf("duplicates report %v, want the sequence numbers the ops already had, %v",
			seqs(replay.Duplicates), seqs(first.Accepted))
	}

	t.Run("a batch half of which is already there", func(t *testing.T) {
		mixed, err := s.Append(t.Context(), id, []ops.Op{anOp("i1", 1), anOp("i3", 3)})
		if err != nil {
			t.Fatalf("appending: %v", err)
		}
		if len(mixed.Duplicates) != 1 || mixed.Duplicates[0].OpID != "i1" {
			t.Errorf("duplicates = %v, want just i1", mixed.Duplicates)
		}
		if len(mixed.Accepted) != 1 || mixed.Accepted[0].OpID != "i3" {
			t.Errorf("accepted = %v, want just i3", mixed.Accepted)
		}
		if mixed.Accepted[0].Seq != first.Head+1 {
			t.Errorf("i3 landed at %d, want %d: a duplicate must not consume a number",
				mixed.Accepted[0].Seq, first.Head+1)
		}
	})

	t.Run("a reused id with different content", func(t *testing.T) {
		impostor := anOp("i1", 1)
		impostor.Fields = map[string]json.RawMessage{"title": json.RawMessage(`"different"`)}
		if _, err := s.Append(t.Context(), id, []ops.Op{impostor}); !errors.Is(err, ErrOpConflict) {
			t.Errorf("Append = %v, want ErrOpConflict", err)
		}
	})

	t.Run("an id repeated inside one request", func(t *testing.T) {
		twice := []ops.Op{anOp("i9", 9), anOp("i9", 9)}
		if _, err := s.Append(t.Context(), id, twice); !errors.Is(err, ErrOpConflict) {
			t.Errorf("Append = %v, want ErrOpConflict", err)
		}
	})
}

// TestAppendRollsBackWithoutLeakingNumbers is why the head is a column and not
// a Postgres SEQUENCE. A sequence hands out numbers that a rollback does not
// give back, which would leave a hole and break the one promise the log makes.
func TestAppendRollsBackWithoutLeakingNumbers(t *testing.T) {
	s := open(t)
	id := aWorkspace(t, s).Workspace.ID

	if _, err := s.Append(t.Context(), id, []ops.Op{anOp("r1", 1)}); err != nil {
		t.Fatalf("appending: %v", err)
	}

	// A batch that fails part way. The good op in front of the bad one must not
	// land, and must not take a number with it.
	failing := []ops.Op{anOp("r2", 2), anOp("r2", 2)}
	if _, err := s.Append(t.Context(), id, failing); err == nil {
		t.Fatal("the failing batch was accepted")
	}

	after, err := s.Append(t.Context(), id, []ops.Op{anOp("r3", 3)})
	if err != nil {
		t.Fatalf("appending: %v", err)
	}
	if after.Accepted[0].Seq != 2 {
		t.Errorf("the next op landed at %d, want 2: the failed batch kept a number", after.Accepted[0].Seq)
	}

	log, head, err := s.Log(t.Context(), id, 0, 100)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	if head != 2 || len(log) != 2 {
		t.Errorf("head = %d with %d ops, want 2 and 2", head, len(log))
	}
}

// TestConcurrentAppendsStayGapless is the claim the whole design is arranged
// around, and the only way to check it is to actually race.
func TestConcurrentAppendsStayGapless(t *testing.T) {
	s := open(t)
	id := aWorkspace(t, s).Workspace.ID

	const writers, each = 8, 12
	var wg sync.WaitGroup
	failures := make(chan error, writers)
	for writer := range writers {
		wg.Go(func() {
			for i := range each {
				op := anOp(fmt.Sprintf("w%d-%d", writer, i), uint64(i)+1)
				if _, err := s.Append(t.Context(), id, []ops.Op{op}); err != nil {
					failures <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatalf("a concurrent append failed: %v", err)
	}

	log, head, err := s.Log(t.Context(), id, 0, writers*each+1)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	if want := int64(writers * each); head != want {
		t.Fatalf("head = %d, want %d", head, want)
	}
	if len(log) != writers*each {
		t.Fatalf("got %d ops, want %d", len(log), writers*each)
	}
	// Every number from 1 to head, once each, in order. That is what gapless
	// means, and it is what lets a client treat the sequence number as a cursor.
	for i, entry := range log {
		if entry.Seq != int64(i)+1 {
			t.Fatalf("op %d has seq %d, want %d", i, entry.Seq, i+1)
		}
	}
}

// TestConcurrentDuplicatesLandOnce races the same op against itself, which is
// what a client retrying while its first attempt is still in flight does.
func TestConcurrentDuplicatesLandOnce(t *testing.T) {
	s := open(t)
	id := aWorkspace(t, s).Workspace.ID
	batch := []ops.Op{anOp("d1", 1), anOp("d2", 2)}

	const attempts = 8
	var wg sync.WaitGroup
	results := make(chan AppendResult, attempts)
	problems := make(chan error, attempts)
	for range attempts {
		wg.Go(func() {
			result, err := s.Append(t.Context(), id, batch)
			if err != nil {
				problems <- err
				return
			}
			results <- result
		})
	}
	wg.Wait()
	close(results)
	close(problems)

	for err := range problems {
		t.Fatalf("a racing retry failed: %v", err)
	}

	written := 0
	for result := range results {
		written += len(result.Accepted)
		if result.Head != 2 {
			t.Errorf("head = %d, want 2 however the race went", result.Head)
		}
	}
	if written != len(batch) {
		t.Errorf("%d ops were written across %d racing attempts, want %d", written, attempts, len(batch))
	}
}

func TestLogPagesInOrder(t *testing.T) {
	s := open(t)
	id := aWorkspace(t, s).Workspace.ID

	const total = 25
	list := make([]ops.Op, total)
	for i := range list {
		list[i] = anOp(fmt.Sprintf("p%02d", i), uint64(i)+1)
	}
	if _, err := s.Append(t.Context(), id, list); err != nil {
		t.Fatalf("appending: %v", err)
	}

	var since int64
	var seen []string
	for range total {
		page, head, err := s.Log(t.Context(), id, since, 7)
		if err != nil {
			t.Fatalf("reading the log: %v", err)
		}
		if head != total {
			t.Fatalf("head = %d, want %d", head, total)
		}
		if len(page) == 0 {
			break
		}
		for _, entry := range page {
			if entry.Seq <= since {
				t.Fatalf("since = %d returned seq %d", since, entry.Seq)
			}
			since = entry.Seq
			seen = append(seen, entry.Op.ID)
		}
	}

	want := make([]string, total)
	for i := range want {
		want[i] = list[i].ID
	}
	if !slices.Equal(seen, want) {
		t.Errorf("paged through %v, want %v", seen, want)
	}
}

// TestOpsSurviveTheRoundTrip is the reason the log is worth storing: what comes
// back has to merge to the same thing as what went in, or every replica drifts.
func TestOpsSurviveTheRoundTrip(t *testing.T) {
	s := open(t)
	id := aWorkspace(t, s).Workspace.ID

	sent := []ops.Op{
		{ID: "t1", Kind: ops.KindCreateNode, Actor: "desktop-6f2a", Clock: 1, Node: "root", Position: "a0V"},
		{ID: "t2", Kind: ops.KindExtractToTask, Actor: "desktop-6f2a", Clock: 2, Node: "root", Task: "task", Parent: "root", Position: "m",
			Fields: map[string]json.RawMessage{
				"title":  json.RawMessage(`"unicode: é 日本語 \" \\ "`),
				"done":   json.RawMessage(`false`),
				"weight": json.RawMessage(`3.5`),
				"tags":   json.RawMessage(`["a","b"]`),
				"empty":  json.RawMessage(`null`),
				"nested": json.RawMessage(`{"deep":{"deeper":[1,2,3]}}`),
			}},
		{ID: "t3", Kind: ops.KindDeleteNode, Actor: "other", Clock: 9, Node: "root"},
	}
	if _, err := s.Append(t.Context(), id, sent); err != nil {
		t.Fatalf("appending: %v", err)
	}

	log, _, err := s.Log(t.Context(), id, 0, 100)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	got := make([]ops.Op, len(log))
	for i, entry := range log {
		got[i] = entry.Op
	}

	// Compared through the merge rather than field by field, because that is
	// what a replica actually does with them.
	var there, back ops.State
	if _, err := there.ApplyAll(sent); err != nil {
		t.Fatalf("merging what was sent: %v", err)
	}
	if _, err := back.ApplyAll(got); err != nil {
		t.Fatalf("merging what came back: %v", err)
	}
	if want, have := mergeDigest(t, &there), mergeDigest(t, &back); want != have {
		t.Errorf("the round trip changed the ops\n got: %s\nwant: %s", have, want)
	}
}

func mergeDigest(t *testing.T, state *ops.State) string {
	t.Helper()
	encoded, err := json.Marshal(struct {
		Tree     []ops.TreeNode `json:"tree"`
		Detached []ops.Node     `json:"detached"`
	}{state.Tree(), state.Detached()})
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	return string(encoded)
}

func TestUnknownWorkspace(t *testing.T) {
	s := open(t)
	if _, err := s.Workspace(t.Context(), "ws_nothere"); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("Workspace = %v, want ErrNoWorkspace", err)
	}
	if _, err := s.Append(t.Context(), "ws_nothere", []ops.Op{anOp("x", 1)}); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("Append = %v, want ErrNoWorkspace", err)
	}
}

func deleteOf(id string, clock uint64, node string) ops.Op {
	return ops.Op{ID: id, Kind: ops.KindDeleteNode, Actor: "test", Clock: clock, Node: node}
}

// editOf is anOp against a named node, for the tests that care which node an op
// touches.
func editOf(id string, clock uint64, node string) ops.Op {
	op := anOp(id, clock)
	op.Node = node
	return op
}

func createOf(id string, clock uint64, node string) ops.Op {
	return ops.Op{ID: id, Kind: ops.KindCreateNode, Actor: "test", Clock: clock, Node: node, Position: "m"}
}

// TestAppendRefusesADeletedTarget covers the only write this server refuses on
// grounds of what is already in the log. The boundaries of it carry as much
// weight as the refusal itself: each subtest below is a case where refusing
// would lose an op that a replica is right to be sending.
func TestAppendRefusesADeletedTarget(t *testing.T) {
	s := open(t)
	id := aWorkspace(t, s).Workspace.ID

	if _, err := s.Append(t.Context(), id, []ops.Op{deleteOf("d1", 5, "gone")}); err != nil {
		t.Fatalf("deleting a node: %v", err)
	}

	var deleted DeletedError
	if _, err := s.Append(t.Context(), id, []ops.Op{editOf("e1", 6, "gone")}); !errors.As(err, &deleted) {
		t.Fatalf("Append = %v, want a DeletedError", err)
	}
	// The error names the op, the node and where the delete sits, because that
	// is what turns a refusal into something an operator can read and a client
	// can go and look up.
	if deleted.OpID != "e1" || deleted.Node != "gone" || deleted.Seq != 1 {
		t.Errorf("DeletedError = %+v, want op e1, node gone, seq 1", deleted)
	}

	t.Run("a higher clock does not help", func(t *testing.T) {
		// Being newer is what wins a contested field. It is not a way past a
		// tombstone: no clock value undoes a delete.
		if _, err := s.Append(t.Context(), id, []ops.Op{editOf("e2", 9_000, "gone")}); !errors.As(err, &deleted) {
			t.Errorf("Append = %v, want a DeletedError however high the clock", err)
		}
	})

	t.Run("the refused batch lands nothing and keeps no number", func(t *testing.T) {
		mixed := []ops.Op{editOf("e3", 7, "alive"), editOf("e4", 8, "gone")}
		if _, err := s.Append(t.Context(), id, mixed); err == nil {
			t.Fatal("a batch with one refused op was accepted")
		}

		// The good op in front of the refused one must not have landed, and
		// must not have taken a sequence number with it.
		next, err := s.Append(t.Context(), id, []ops.Op{editOf("e5", 9, "alive")})
		if err != nil {
			t.Fatalf("appending after a refusal: %v", err)
		}
		if next.Accepted[0].Seq != 2 {
			t.Errorf("the next op landed at %d, want 2: the refused batch left a hole",
				next.Accepted[0].Seq)
		}
	})

	t.Run("deleting what is already deleted is accepted", func(t *testing.T) {
		// The outcome asked for is the outcome already. Refusing would leave a
		// client retrying an op it has no other way to be rid of.
		result, err := s.Append(t.Context(), id, []ops.Op{deleteOf("d2", 10, "gone")})
		if err != nil {
			t.Fatalf("deleting a deleted node: %v", err)
		}
		if len(result.Accepted) != 1 {
			t.Errorf("accepted = %v, want the second delete written", result.Accepted)
		}
	})

	t.Run("a move under a deleted parent is accepted", func(t *testing.T) {
		// The node lands in the document's detached list, which is a state the
		// viewer renders rather than an error. Only the node being moved has to
		// be alive.
		move := ops.Op{ID: "m1", Kind: ops.KindMoveNode, Actor: "test", Clock: 11,
			Node: "alive", Parent: "gone", Position: "m"}
		if _, err := s.Append(t.Context(), id, []ops.Op{move}); err != nil {
			t.Errorf("moving under a deleted parent: %v", err)
		}
	})

	t.Run("a retry of an op that landed before the delete is still a duplicate", func(t *testing.T) {
		// The server has this op. Answering with a refusal would make a client
		// drop a write that is in the log, which is the one answer no amount of
		// reconciling recovers from.
		other := aWorkspace(t, s).Workspace.ID
		edit := editOf("first", 1, "n")
		if _, err := s.Append(t.Context(), other, []ops.Op{edit}); err != nil {
			t.Fatalf("appending: %v", err)
		}
		if _, err := s.Append(t.Context(), other, []ops.Op{deleteOf("then", 2, "n")}); err != nil {
			t.Fatalf("deleting: %v", err)
		}

		replay, err := s.Append(t.Context(), other, []ops.Op{edit})
		if err != nil {
			t.Fatalf("replaying an op the log already holds: %v", err)
		}
		if len(replay.Duplicates) != 1 || replay.Duplicates[0].Seq != 1 {
			t.Errorf("duplicates = %v, want the op reported at the seq it has", replay.Duplicates)
		}
	})

	t.Run("a node deleted inside the same request does not refuse the rest of it", func(t *testing.T) {
		// An offline replica's outbox holds a whole session: a node created,
		// edited, then deleted. All of it has to be pushable, and the order the
		// client happened to serialise it in must not decide the answer — the
		// log promises not to care about that order.
		outbox := []ops.Op{createOf("c", 1, "n"), editOf("e", 2, "n"), deleteOf("d", 3, "n")}
		for _, order := range []struct {
			name  string
			batch []ops.Op
		}{
			{"the delete last", outbox},
			{"the delete first", []ops.Op{outbox[2], outbox[1], outbox[0]}},
		} {
			t.Run(order.name, func(t *testing.T) {
				fresh := aWorkspace(t, s).Workspace.ID
				result, err := s.Append(t.Context(), fresh, order.batch)
				if err != nil {
					t.Fatalf("pushing an outbox: %v", err)
				}
				if len(result.Accepted) != len(order.batch) {
					t.Errorf("accepted %d of %d ops", len(result.Accepted), len(order.batch))
				}
			})
		}
	})

	t.Run("a create the delete overtook is refused", func(t *testing.T) {
		// The merge tombstones a node no op has mentioned yet, so the node is
		// deleted whether the create arrives or not. Storing the late create
		// would add an op that changes nothing anyone can see; refusing it
		// tells the replica the node is gone.
		fresh := aWorkspace(t, s).Workspace.ID
		if _, err := s.Append(t.Context(), fresh, []ops.Op{deleteOf("d", 9, "n")}); err != nil {
			t.Fatalf("deleting: %v", err)
		}
		if _, err := s.Append(t.Context(), fresh, []ops.Op{createOf("c", 1, "n")}); !errors.As(err, &deleted) {
			t.Errorf("Append = %v, want a DeletedError", err)
		}
	})

	t.Run("extraction cares about the task, not the source", func(t *testing.T) {
		fresh := aWorkspace(t, s).Workspace.ID
		if _, err := s.Append(t.Context(), fresh, []ops.Op{deleteOf("d", 5, "source")}); err != nil {
			t.Fatalf("deleting: %v", err)
		}

		// Extracting from a node deleted meanwhile still produces the task and
		// still links back, so the tombstone records where its content went.
		extract := ops.Op{ID: "x1", Kind: ops.KindExtractToTask, Actor: "test", Clock: 6,
			Node: "source", Task: "task", Position: "m"}
		if _, err := s.Append(t.Context(), fresh, []ops.Op{extract}); err != nil {
			t.Errorf("extracting from a deleted node was refused: %v", err)
		}

		if _, err := s.Append(t.Context(), fresh, []ops.Op{deleteOf("d2", 7, "dead-task")}); err != nil {
			t.Fatalf("deleting: %v", err)
		}
		onDeadTask := extract
		onDeadTask.ID, onDeadTask.Clock, onDeadTask.Task = "x2", 8, "dead-task"
		if _, err := s.Append(t.Context(), fresh, []ops.Op{onDeadTask}); !errors.As(err, &deleted) {
			t.Errorf("Append = %v, want a DeletedError for the task node", err)
		}
	})
}

// TestAppendNeverRefusesAStaleWrite is the offline story stated as a test. The
// server is the merge point and the ordering authority, not a version the
// desktop has to be level with: there is nothing to be behind, so being behind
// cannot be a reason to lose a write.
func TestAppendNeverRefusesAStaleWrite(t *testing.T) {
	s := open(t)
	id := aWorkspace(t, s).Workspace.ID

	const ahead = 50
	current := make([]ops.Op, ahead)
	for i := range current {
		current[i] = editOf(fmt.Sprintf("ahead-%02d", i), uint64(i)+100, "n")
	}
	if _, err := s.Append(t.Context(), id, current); err != nil {
		t.Fatalf("appending: %v", err)
	}

	// A replica that has not seen any of that, carrying the lowest clock there
	// is. Whether the op wins a field is the merge's business; whether it is
	// stored is this server's, and the answer is yes.
	result, err := s.Append(t.Context(), id, []ops.Op{editOf("behind", 1, "n")})
	if err != nil {
		t.Fatalf("a write from a replica that is behind was refused: %v", err)
	}
	if len(result.Accepted) != 1 || result.Accepted[0].Seq != ahead+1 {
		t.Errorf("accepted = %v, want the op written at seq %d", result.Accepted, ahead+1)
	}
}
