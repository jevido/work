package workbench

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestConflictCoversDirectoriesAndGlobs(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"a/b.go", "a/b.go", true},
		{"a/b.go", "a/c.go", false},
		{"a", "a/b.go", true},
		{"a/b.go", "a", true},
		{"a/b", "a/bc/d.go", false}, // prefix, but not the same directory
		{"a/*.go", "a/b.go", true},
		{"a/*.go", "a/b/c.go", false}, // * does not cross a slash
		{"a/**", "a/b/c.go", true},
		{"a/**", "b/c.go", false},
		{"a/*.go", "a/*.ts", false}, // two patterns are only compared literally
		{"", "a/b.go", false},
	}
	for _, c := range cases {
		if got := conflict(c.a, c.b); got != c.want {
			t.Errorf("conflict(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestCleanPathsNormalisesAndDrops(t *testing.T) {
	got := cleanPaths([]string{
		" ./a/b.go ", "a/b.go", "/a/c.go", "`a/d.go`", "a/e/", "a/./f.go",
		"", "..", "../outside.go", "a/*.ts",
	})
	want := []string{"a/b.go", "a/c.go", "a/d.go", "a/e", "a/f.go", "a/*.ts"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanPaths = %q, want %q", got, want)
	}
}

func TestTakeIsAllOrNothing(t *testing.T) {
	c := newClaims([]PlanStep{
		{AgentID: "chris", Files: []string{"a.go", "b.go"}},
		{AgentID: "dennis", Files: []string{"b.go", "c.go"}},
	})

	if blocker, ok := c.take("chris", []string{"a.go", "b.go"}); !ok {
		t.Fatalf("first take refused by %q", blocker)
	}
	blocker, ok := c.take("dennis", []string{"b.go", "c.go"})
	if ok {
		t.Fatal("overlapping take allowed")
	}
	if blocker != "chris" {
		t.Fatalf("blocked by %q, want chris", blocker)
	}
	// Nothing was taken, so the file it could have had is still free: a step
	// never holds part of its claim while it waits.
	if holder := c.holder("jeff", []string{"c.go"}); holder != "" {
		t.Fatalf("c.go held by %q after a refused take", holder)
	}

	c.finish("chris")
	if blocker, ok := c.take("dennis", []string{"b.go", "c.go"}); !ok {
		t.Fatalf("take after finish refused by %q", blocker)
	}
}

func TestNoFilesNeverBlocks(t *testing.T) {
	c := newClaims([]PlanStep{{AgentID: "chris", Files: []string{"a.go"}}})
	c.take("chris", []string{"a.go"})
	if _, ok := c.take("jeff", nil); !ok {
		t.Fatal("a step claiming nothing was blocked")
	}
}

func TestPendingNamesTheOwnerWhoHasNotStarted(t *testing.T) {
	c := newClaims([]PlanStep{
		{AgentID: "chris", Files: []string{"a.go"}},
		{AgentID: "dennis", Files: []string{"b.go"}},
	})

	// Dennis has not taken b.go yet, but the plan says it is his, so a step
	// that reports itself blocked on it has something real to wait for.
	if got := c.pending("chris", []string{"b.go"}); got != "dennis" {
		t.Fatalf("pending = %q, want dennis", got)
	}
	// A queueing agent owns nothing, so it is never the thing to wait for.
	c.wait("dennis")
	if got := c.pending("chris", []string{"b.go"}); got != "" {
		t.Fatalf("pending = %q while dennis waits, want empty", got)
	}
	c.take("dennis", []string{"b.go"})
	if got := c.pending("chris", []string{"b.go"}); got != "dennis" {
		t.Fatalf("pending = %q once dennis holds it, want dennis", got)
	}
	c.finish("dennis")
	if got := c.pending("chris", []string{"b.go"}); got != "" {
		t.Fatalf("pending = %q once dennis is done, want empty", got)
	}
}

func TestAwaitFilesQueuesBehindTheHolder(t *testing.T) {
	c := newClaims([]PlanStep{
		{AgentID: "chris", Files: []string{"shared.go"}},
		{AgentID: "dennis", Files: []string{"shared.go"}},
	})
	c.take("chris", []string{"shared.go"})

	w := &Workbench{}
	done := make(chan string, 1)
	go func() {
		waited, ok := w.awaitFiles(
			context.Background(), "dennis", []string{"shared.go"}, "", c)
		if !ok {
			t.Error("awaitFiles reported cancellation")
		}
		done <- waited
	}()

	select {
	case <-done:
		t.Fatal("awaitFiles returned while the file was held")
	case <-time.After(20 * time.Millisecond):
	}

	c.finish("chris")
	select {
	case waited := <-done:
		if waited != "chris" {
			t.Fatalf("waited on %q, want chris", waited)
		}
	case <-time.After(time.Second):
		t.Fatal("awaitFiles did not return after the file came free")
	}
}

func TestAwaitFilesStopsOnCancel(t *testing.T) {
	c := newClaims([]PlanStep{{AgentID: "chris", Files: []string{"shared.go"}}})
	c.take("chris", []string{"shared.go"})

	w := &Workbench{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() {
		_, ok := w.awaitFiles(ctx, "dennis", []string{"shared.go"}, "", c)
		done <- ok
	}()

	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("awaitFiles succeeded after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("awaitFiles ignored cancellation")
	}
}

// Two steps claiming the same file must never both be inside it, however the
// scheduling falls out.
func TestOverlappingStepsNeverOverlapInTime(t *testing.T) {
	c := newClaims([]PlanStep{
		{AgentID: "chris", Files: []string{"frontend/renderer.ts"}},
		{AgentID: "dennis", Files: []string{"frontend"}},
		{AgentID: "jeff", Files: []string{"backend/main.go"}},
	})
	w := &Workbench{}

	var mu sync.Mutex
	inside := map[string]bool{}
	peak := 0

	var wg sync.WaitGroup
	for _, step := range []PlanStep{
		{AgentID: "chris", Files: []string{"frontend/renderer.ts"}},
		{AgentID: "dennis", Files: []string{"frontend"}},
	} {
		wg.Add(1)
		go func(step PlanStep) {
			defer wg.Done()
			if _, ok := w.awaitFiles(
				context.Background(), step.AgentID, step.Files, "", c); !ok {
				return
			}
			mu.Lock()
			inside[step.AgentID] = true
			if len(inside) > peak {
				peak = len(inside)
			}
			mu.Unlock()

			time.Sleep(10 * time.Millisecond)

			mu.Lock()
			delete(inside, step.AgentID)
			mu.Unlock()
			c.finish(step.AgentID)
		}(step)
	}
	wg.Wait()

	if peak != 1 {
		t.Fatalf("%d agents were in frontend/renderer.ts at once, want 1", peak)
	}
}

func TestBlockedPathsReadsTheMarker(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []string
	}{
		{"clean answer", "Done. Nothing left.", nil},
		{
			"marker last",
			"Got the layout done.\n\nBLOCKED: internal/a.go internal/b.go\n",
			[]string{"internal/a.go", "internal/b.go"},
		},
		{"lowercase", "stuck\nblocked: a.go", []string{"a.go"}},
		{"comma separated", "stuck\nBLOCKED: a.go, b.go", []string{"a.go", "b.go"}},
		{"fenced", "stuck\n`BLOCKED: a.go`", []string{"a.go"}},
		{
			"explained mid-answer does not count",
			"I would say BLOCKED: a.go if I were stuck.\n\nBut I finished it.",
			nil,
		},
		{"no paths", "stuck\nBLOCKED:", nil},
		{"empty", "", nil},
	}
	for _, c := range cases {
		got := blockedPaths(c.out)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: blockedPaths = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestBlockedNoteNamesTheColleague(t *testing.T) {
	got := blockedNote("chris", []string{"a.go", "b.go"})
	want := "waiting on chris to finish with a.go, b.go"
	if got != want {
		t.Fatalf("blockedNote = %q, want %q", got, want)
	}
	if got := blockedNote("", []string{"a.go"}); got != "waiting on a.go" {
		t.Fatalf("blockedNote with no holder = %q", got)
	}
}

// Two steps that each report themselves blocked on the other's files must both
// get through. A step gives its own files back before it queues, so the
// colleague it is waiting on is never waiting for it in turn.
func TestTwoBlockedStepsDoNotDeadlock(t *testing.T) {
	steps := []PlanStep{
		{AgentID: "chris", Files: []string{"a.go"}},
		{AgentID: "dennis", Files: []string{"b.go"}},
	}
	c := newClaims(steps)
	w := &Workbench{}

	c.take("chris", []string{"a.go"})
	c.take("dennis", []string{"b.go"})

	var mu sync.Mutex
	inside := 0
	peak := 0

	// Each one now discovers it needs the other's file, so it gives its own
	// back and queues for both.
	var wg sync.WaitGroup
	for i, pair := range [][2][]string{
		{{"a.go"}, {"b.go"}}, // chris owns a, is blocked on b
		{{"b.go"}, {"a.go"}}, // dennis owns b, is blocked on a
	} {
		wg.Add(1)
		go func(agentID string, own, want []string) {
			defer wg.Done()
			defer c.finish(agentID)

			c.wait(agentID)
			if _, ok := w.awaitFiles(
				context.Background(), agentID,
				append(append([]string{}, own...), want...), "", c); !ok {
				t.Errorf("%s was cancelled", agentID)
				return
			}

			mu.Lock()
			inside++
			if inside > peak {
				peak = inside
			}
			mu.Unlock()

			time.Sleep(5 * time.Millisecond)

			mu.Lock()
			inside--
			mu.Unlock()
		}(steps[i].AgentID, pair[0], pair[1])
	}

	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("both steps are still waiting: deadlock")
	}

	// Both got through, and never at the same time: each wanted both files.
	if peak != 1 {
		t.Fatalf("%d steps held both files at once, want 1", peak)
	}
}
