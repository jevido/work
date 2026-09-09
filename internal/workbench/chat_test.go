package workbench

import (
	"context"
	"strings"
	"testing"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/claude"
)

// newChatWorkbench builds a workbench whose CLI lookup succeeds without any
// test spawning Claude: every case here returns before Run is reached.
func newChatWorkbench(t *testing.T) *Workbench {
	t.Helper()
	return New(agents.Default(), claude.NewRunner("sh"), func(string, any) {}, t.TempDir())
}

// The side channel and the run are separate conversations. If they shared one,
// two live Claude processes would resume the same session.
func TestChatSessionIsSeparateFromRunSession(t *testing.T) {
	w := newChatWorkbench(t)

	w.rememberSession(runSession("anton"), "run-session")
	w.rememberSession(chatSession("anton"), "chat-session")

	if got := w.sessionFor(runSession("anton")); got != "run-session" {
		t.Errorf("run session = %q, want %q", got, "run-session")
	}
	if got := w.sessionFor(chatSession("anton")); got != "chat-session" {
		t.Errorf("chat session = %q, want %q", got, "chat-session")
	}

	// Forgetting one must not forget the other: a run whose session went bad
	// should not take the conversation the user is holding down with it.
	w.forgetSession(runSession("anton"))
	if got := w.sessionFor(chatSession("anton")); got != "chat-session" {
		t.Errorf("chat session = %q after forgetting the run's, want it kept", got)
	}
}

// An agent ID is a folder name, so it can contain anything a folder can. No
// name may reach another agent's side channel.
func TestChatSessionCannotBeForgedByAgentName(t *testing.T) {
	w := newChatWorkbench(t)

	w.rememberSession(chatSession("anton"), "anton-chat")
	// A folder called "anton#chat" is a legal folder.
	w.rememberSession(runSession("anton#chat"), "impostor")

	if got := w.sessionFor(chatSession("anton")); got != "anton-chat" {
		t.Errorf("chat session = %q, want %q", got, "anton-chat")
	}
}

// The side channel has its own history, so it has to be told what the run is
// doing or it is answering "how is it going" about nothing.
func TestChatPromptCarriesTheWorkInFlight(t *testing.T) {
	got := chatPrompt("how far along is Chris?", "overhaul the office canvas")
	if !strings.Contains(got, "how far along is Chris?") {
		t.Error("the question itself is missing")
	}
	if !strings.Contains(got, "overhaul the office canvas") {
		t.Error("the run being asked about is missing")
	}
	if !strings.Contains(got, "do not start") {
		t.Error("nothing tells the coordinator this channel does not dispatch")
	}

	// Nothing running: the question stands on its own, with no invented context.
	if got := chatPrompt("what can you do?", ""); got != "what can you do?" {
		t.Errorf("chatPrompt with no run = %q, want the bare question", got)
	}
}

// One answer at a time, and a refusal must not disturb the answer in flight.
func TestChatRefusesASecondQuestion(t *testing.T) {
	w := newChatWorkbench(t)
	first := &run{id: "c1", cancel: func() {}, chat: true}
	w.mu.Lock()
	w.chat = first
	w.mu.Unlock()

	if _, err := w.Chat("and another thing"); err == nil {
		t.Fatal("want an error while an answer is in flight")
	}

	w.mu.Lock()
	held := w.chat
	w.mu.Unlock()
	if held != first {
		t.Error("the refused question replaced the one in flight")
	}
}

// The point of the whole change: a question asked while work is in flight is
// accepted, where Submit would refuse it.
func TestChatIsAcceptedWhileARunIsActive(t *testing.T) {
	events := make(chan string, 8)
	w := New(agents.Default(), claude.NewRunner("sh"), func(name string, _ any) {
		select {
		case events <- name:
		default:
		}
	}, t.TempDir())

	// A run holds the slot, as it would while specialists are working.
	_, runCancel := context.WithCancel(context.Background())
	t.Cleanup(runCancel)
	w.mu.Lock()
	w.active = &run{id: "r1", prompt: "overhaul the office canvas", cancel: runCancel}
	w.mu.Unlock()

	// Submit is the behaviour being worked around: it refuses.
	if _, err := w.Submit("", "and another thing"); err == nil {
		t.Fatal("Submit accepted a second run; the limit it works around is gone")
	}

	task, err := w.Chat("how far along is Chris?")
	if err != nil {
		t.Fatalf("Chat while a run is active: %v", err)
	}
	if task.ID == "" {
		t.Error("Chat returned no task ID to cancel with")
	}
	t.Cleanup(func() { _ = w.Cancel(task.ID) })

	if got := <-events; got != EventChatStarted {
		t.Errorf("first event = %q, want %q", got, EventChatStarted)
	}

	// The run still holds its own slot: the question borrowed nothing.
	w.mu.Lock()
	active := w.active
	w.mu.Unlock()
	if active == nil || active.id != "r1" {
		t.Error("the question disturbed the active run")
	}
}

// Stopping a question must not stop the work it was asking about.
func TestCancelChatLeavesTheRunAlone(t *testing.T) {
	w := newChatWorkbench(t)

	runCtx, runCancel := context.WithCancel(context.Background())
	chatCtx, chatCancel := context.WithCancel(context.Background())
	t.Cleanup(runCancel)
	t.Cleanup(chatCancel)

	w.mu.Lock()
	w.active = &run{id: "r1", cancel: runCancel}
	w.chat = &run{id: "c1", cancel: chatCancel, chat: true}
	w.mu.Unlock()

	if err := w.Cancel("c1"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if chatCtx.Err() == nil {
		t.Error("cancelling the question did not stop it")
	}
	if runCtx.Err() != nil {
		t.Error("cancelling the question stopped the run too")
	}
}

// And the other direction: stopping the work leaves the conversation alone.
func TestCancelRunLeavesChatAlone(t *testing.T) {
	w := newChatWorkbench(t)

	runCtx, runCancel := context.WithCancel(context.Background())
	chatCtx, chatCancel := context.WithCancel(context.Background())
	t.Cleanup(runCancel)
	t.Cleanup(chatCancel)

	w.mu.Lock()
	w.active = &run{id: "r1", cancel: runCancel}
	w.chat = &run{id: "c1", cancel: chatCancel, chat: true}
	w.mu.Unlock()

	if err := w.Cancel("r1"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if runCtx.Err() == nil {
		t.Error("cancelling the run did not stop it")
	}
	if chatCtx.Err() != nil {
		t.Error("cancelling the run stopped the question too")
	}
}

// Reloading reconciles per-agent bookkeeping. A chat session belongs to an
// agent, so it survives exactly as long as that agent does.
func TestReloadKeepsChatSessionForSurvivingAgent(t *testing.T) {
	w := newChatWorkbench(t)
	root := t.TempDir()
	if _, err := w.UseConfigRoot(root); err != nil {
		t.Fatalf("UseConfigRoot: %v", err)
	}

	coordinator, ok := w.registry.Coordinator()
	if !ok {
		t.Fatal("no coordinator")
	}
	w.rememberSession(chatSession(coordinator.ID), "chat-session")
	w.rememberSession(chatSession("departed"), "gone-session")

	if _, err := w.ReloadAgents(); err != nil {
		t.Fatalf("ReloadAgents: %v", err)
	}

	if got := w.sessionFor(chatSession(coordinator.ID)); got != "chat-session" {
		t.Errorf("chat session = %q after reload, want it kept", got)
	}
	if got := w.sessionFor(chatSession("departed")); got != "" {
		t.Errorf("chat session for a removed agent = %q, want it dropped", got)
	}
}

// Reloading mid-answer would leave the board and the office describing a team
// that does not match what is in flight, exactly as it would mid-run.
func TestReloadRefusedWhileAnswering(t *testing.T) {
	w := newChatWorkbench(t)
	root := t.TempDir()
	if _, err := w.UseConfigRoot(root); err != nil {
		t.Fatalf("UseConfigRoot: %v", err)
	}

	w.mu.Lock()
	w.chat = &run{id: "c1", cancel: func() {}, chat: true}
	w.mu.Unlock()

	if _, err := w.ReloadAgents(); err == nil {
		t.Fatal("want an error while a question is being answered")
	}
}
