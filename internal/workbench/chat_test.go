package workbench

import (
	"context"
	"strings"
	"testing"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/claude"
)

// in names the transcript a test is talking in: one tab, one mode.
//
// A helper because almost every assertion here is about a session key, and a
// struct literal per call would bury what the test is actually checking under
// the same three tokens each time. The tab is fixed and arbitrary; the tests
// that care which tab it is say so themselves.
func in(mode string) Conversation { return Conversation{TabID: "tab-1", Mode: mode} }

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

	w.rememberSession(runSession("anton", in(ModeWork)), "run-session")
	w.rememberSession(chatSession("anton", in(ModeWork)), "chat-session")

	if got := w.sessionFor(runSession("anton", in(ModeWork))); got != "run-session" {
		t.Errorf("run session = %q, want %q", got, "run-session")
	}
	if got := w.sessionFor(chatSession("anton", in(ModeWork))); got != "chat-session" {
		t.Errorf("chat session = %q, want %q", got, "chat-session")
	}

	// Forgetting one must not forget the other: a run whose session went bad
	// should not take the conversation the user is holding down with it.
	w.forgetSession(runSession("anton", in(ModeWork)))
	if got := w.sessionFor(chatSession("anton", in(ModeWork))); got != "chat-session" {
		t.Errorf("chat session = %q after forgetting the run's, want it kept", got)
	}
}

// An agent ID is a folder name, so it can contain anything a folder can. No
// name may reach another agent's side channel.
func TestChatSessionCannotBeForgedByAgentName(t *testing.T) {
	w := newChatWorkbench(t)

	w.rememberSession(chatSession("anton", in(ModeWork)), "anton-chat")
	// A folder called "anton#chat" is a legal folder.
	w.rememberSession(runSession("anton#chat", in(ModeWork)), "impostor")

	if got := w.sessionFor(chatSession("anton", in(ModeWork))); got != "anton-chat" {
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

	if _, err := w.Chat(in(ModeWork), "and another thing"); err == nil {
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
	if _, err := w.Submit(in(ModeWork), "", "and another thing"); err == nil {
		t.Fatal("Submit accepted a second run; the limit it works around is gone")
	}

	task, err := w.Chat(in(ModeWork), "how far along is Chris?")
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
	w.rememberSession(chatSession(coordinator.ID, in(ModeWork)), "chat-session")
	w.rememberSession(chatSession("departed", in(ModeWork)), "gone-session")

	if _, err := w.ReloadAgents(); err != nil {
		t.Fatalf("ReloadAgents: %v", err)
	}

	if got := w.sessionFor(chatSession(coordinator.ID, in(ModeWork))); got != "chat-session" {
		t.Errorf("chat session = %q after reload, want it kept", got)
	}
	if got := w.sessionFor(chatSession("departed", in(ModeWork))); got != "" {
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

// A conversation per mode, underneath the transcripts.
//
// Splitting only what is on screen would look fixed and not be: the three
// transcripts would read as separate while the model continued one session with
// all three in its history.
func TestChatSessionsAreSplitByMode(t *testing.T) {
	stateHome(t)
	w := New(agents.Default(), claude.NewRunner(""), func(string, any) {}, t.TempDir())

	w.rememberSession(chatSession("anton", in(ModeIdea)), "about-shape")
	w.rememberSession(chatSession("anton", in(ModePlanning)), "about-tasks")
	w.rememberSession(chatSession("anton", in(ModeWork)), "about-the-run")

	for _, want := range []struct{ mode, session string }{
		{ModeIdea, "about-shape"},
		{ModePlanning, "about-tasks"},
		{ModeWork, "about-the-run"},
	} {
		if got := w.sessionFor(chatSession("anton", in(want.mode))); got != want.session {
			t.Errorf("%s resumed %q, want %q", want.mode, got, want.session)
		}
	}

	// And two questions in one mode are one conversation, which is the half
	// that makes it a conversation at all.
	if a, b := chatSession("anton", in(ModeIdea)), chatSession("anton", in(ModeIdea)); a != b {
		t.Error("two chats in one mode take different keys")
	}

	// Anything that is not one of the two thinking modes is work's, where the
	// side channel has always lived. An unknown mode must not mint a fourth
	// conversation nobody can see.
	if got := w.sessionFor(chatSession("anton", in("sideways"))); got != "about-the-run" {
		t.Errorf("an unknown mode resumed %q, want work's", got)
	}

	// A run and a chat are two conversations even in one transcript: the side
	// channel exists so a question can be asked while work is in flight, and
	// the two resuming each other is exactly what that would undo.
	if runSession("anton", in(ModeWork)) == chatSession("anton", in(ModeWork)) {
		t.Error("a run and a chat share a key")
	}
}

// And a conversation per tab, for the same reason there is one per mode.
//
// Two tabs both in work mode used to share a transcript and a session, so a
// question asked about one project was answered out of the history of the
// other -- with the run that produced that history having executed in a
// different folder entirely.
func TestConversationsAreSplitByTab(t *testing.T) {
	stateHome(t)
	w := New(agents.Default(), claude.NewRunner(""), func(string, any) {}, t.TempDir())

	here := Conversation{TabID: "tab-1", Mode: ModeWork}
	there := Conversation{TabID: "tab-2", Mode: ModeWork}

	w.rememberSession(chatSession("anton", here), "about-this-project")
	w.rememberSession(chatSession("anton", there), "about-that-one")
	w.rememberSession(runSession("anton", here), "run-here")
	w.rememberSession(runSession("anton", there), "run-there")

	if got := w.sessionFor(chatSession("anton", here)); got != "about-this-project" {
		t.Errorf("tab-1 resumed %q, want its own", got)
	}
	if got := w.sessionFor(chatSession("anton", there)); got != "about-that-one" {
		t.Errorf("tab-2 resumed %q, want its own", got)
	}
	// The run keys too. They did not carry a tab until transcripts could be
	// cleared one at a time; a run filed under a key its transcript's New chat
	// cannot reach outlives the conversation it belongs to.
	if got := w.sessionFor(runSession("anton", here)); got != "run-here" {
		t.Errorf("tab-1's run resumed %q, want its own", got)
	}
	if got := w.sessionFor(runSession("anton", there)); got != "run-there" {
		t.Errorf("tab-2's run resumed %q, want its own", got)
	}

	// A tab id is a node id, and a separator is a legal character in one. The
	// key is a struct so there is nothing to forge by naming a tab carefully.
	forged := Conversation{TabID: "tab-1\x00work", Mode: ModeWork}
	if got := w.sessionFor(chatSession("anton", forged)); got != "" {
		t.Errorf("a forged tab id reached %q", got)
	}
}
