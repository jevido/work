package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"dev.jevido/work/internal/agents"
	"dev.jevido/work/internal/board"
	"dev.jevido/work/internal/changes"
	"dev.jevido/work/internal/claude"
	"dev.jevido/work/internal/config"
)

// Emitter delivers a named event with a payload to the frontend. main wires
// this to the Wails event manager; tests pass a fake.
type Emitter func(name string, data any)

// cancelledMessage marks an agent:finished event that came from a cancellation
// rather than a completed task. The frontend uses it to skip the "finished"
// flourish and send the agent straight back to wandering.
const cancelledMessage = "cancelled"

// Task identifies the work started by one Submit call.
type Task struct {
	// ID is the run ID. Cancel takes this.
	ID string `json:"id"`
	// Prompt is the user's request.
	Prompt string `json:"prompt"`
	// AgentID is the agent the request went to: the coordinator for a routed
	// run, or the named agent for a direct one.
	AgentID string `json:"agentId"`
	// Routed is true when Anton will decide who does the work.
	Routed bool `json:"routed"`
}

// AgentStatus is the frontend-visible state of one agent.
type AgentStatus struct {
	agents.Agent
	State  AgentState `json:"state"`
	TaskID string     `json:"taskId,omitempty"`
}

// Workbench coordinates agents and their Claude processes.
//
// One run is active at a time. That is a deliberate limit: the console shows a
// single conversation, and a second concurrent run would make both the output
// and the office unreadable. Within a run, specialists do work in parallel.
//
// The coordinator's side channel is the one exception. Chat runs alongside an
// active run so the user is never locked out of asking what is going on, and it
// earns that by touching nothing the run owns: no board card, no working-tree
// snapshot, no agent state, and its own Claude session. It talks; it does not
// take over. Redirecting the work is still Cancel and a new run.
type Workbench struct {
	registry *agents.Registry
	runner   *claude.Runner
	emit     Emitter
	workDir  string
	board    *board.Board

	nextRun  atomic.Uint64
	nextChat atomic.Uint64

	// admit serialises the decision to start a run. It is held for the whole
	// of Submit, and by Redirect across cancel-wait-start, so no other caller
	// can slip into the free slot in between. Never held together with mu, and
	// never taken by a run's own goroutine, so waiting on a run while holding
	// it cannot deadlock.
	admit sync.Mutex

	// chatAdmit serialises the decision to start a side-channel answer, the
	// same way admit does for runs. It is a separate lock on purpose: a
	// question to the coordinator must not queue behind the run it is about.
	chatAdmit sync.Mutex

	mu      sync.Mutex
	state   map[string]AgentState
	current map[string]string // agentID -> taskID
	// active is the running run, or nil. Guarded by mu.
	active *run
	// chat is the side-channel answer in flight, or nil. Guarded by mu. It is
	// deliberately not the same slot as active: the two are independent, and
	// one finishing must never release the other.
	chat *run

	// sessions is each agent's Claude session for the current conversation,
	// so a follow-up turn continues where the last one left off instead of
	// starting from nothing. Keyed by agent ID.
	sessions map[sessionKey]string
	// turns counts user requests in this conversation. Anton is told when he
	// is answering a follow-up rather than an opening question.
	turns int
	// chatTurns counts side-channel questions, separately: the side channel is
	// its own conversation with its own history, so its follow-ups are not the
	// run's follow-ups.
	chatTurns int
	// review holds the working tree as it was before the last run, so its file
	// changes can be listed and reverted afterwards.
	review *changes.Snapshot
	// root is the config folder agents are loaded from, empty until the user
	// picks one. Until then the built-in team stands in, so Work is usable
	// before it is configured.
	root string
	// permission is what every agent is allowed to do, unless their own
	// definition narrows it. Held here rather than on each agent because it is
	// one answer for the whole workbench: the toggle that changes it is about
	// this session's appetite for risk, not about who Anton is.
	permission claude.PermissionMode
}

// run is one top-level request, from prompt to final answer.
type run struct {
	id     string
	prompt string
	cancel context.CancelFunc
	nextID atomic.Uint64
	// agentID is the lead: the coordinator for a routed run, the named agent
	// for a direct one. Redirect reports it back to the caller.
	agentID string
	// followUp is true when this is not the first request of the conversation.
	followUp bool
	// done is closed by finishRun once the run has released the workbench and
	// emitted its last event. Redirect waits on it, so cancelling and starting
	// again is a single ordered handover rather than a race.
	done chan struct{}
	// chat marks a side-channel answer. It suppresses the agent:* state
	// changes a normal turn emits: the office seats an agent per task, and the
	// coordinator answering a question while also leading a run would
	// otherwise fight himself over one desk and one entry in current.
	chat bool
}

// taskID mints a stable, readable ID for one Claude call inside the run.
func (r *run) taskID(suffix string) string {
	return r.id + "." + suffix + strconv.FormatUint(r.nextID.Add(1), 10)
}

// New returns a Workbench. workDir is the directory Claude runs in.
func New(reg *agents.Registry, runner *claude.Runner, emit Emitter, workDir string) *Workbench {
	w := &Workbench{
		registry: reg,
		runner:   runner,
		emit:     emit,
		workDir:  workDir,
		board:    board.New(),
		state:    make(map[string]AgentState),
		current:  make(map[string]string),
		sessions: make(map[sessionKey]string),
		// Something has to be chosen. Inheriting the user's own Claude
		// configuration was the old behaviour and it meant agents that could
		// not act, since nothing here can answer a permission prompt.
		permission: claude.DefaultPermission,
	}
	for _, a := range reg.All() {
		w.state[a.ID] = StateIdle
	}
	return w
}

// Agents returns every agent with its current state.
func (w *Workbench) Agents() []AgentStatus {
	all := w.registry.All()
	out := make([]AgentStatus, 0, len(all))

	w.mu.Lock()
	defer w.mu.Unlock()
	for _, a := range all {
		out = append(out, AgentStatus{
			Agent:  a,
			State:  w.state[a.ID],
			TaskID: w.current[a.ID],
		})
	}
	return out
}

// Submit starts a run.
//
// An empty agentID gives the task to the coordinator, who routes it: he either
// answers himself or splits it between specialists. A named agentID skips
// routing and puts that one agent straight to work.
func (w *Workbench) Submit(agentID, prompt string) (Task, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return Task{}, errors.New("workbench: empty prompt")
	}
	if _, err := w.runner.Available(); err != nil {
		return Task{}, fmt.Errorf("workbench: local claude CLI not found on PATH: %w", err)
	}

	routed := agentID == ""
	var lead agents.Agent
	if routed {
		c, ok := w.registry.Coordinator()
		if !ok {
			return Task{}, errors.New("workbench: no coordinator configured")
		}
		lead = c
	} else {
		a, ok := w.registry.Get(agentID)
		if !ok {
			return Task{}, fmt.Errorf("workbench: unknown agent %q", agentID)
		}
		lead = a
	}

	ctx, cancel := context.WithCancel(context.Background())
	r := &run{
		id:     "r" + strconv.FormatUint(w.nextRun.Add(1), 10),
		prompt: prompt,
		cancel: cancel,
	}

	w.mu.Lock()
	if w.active != nil {
		w.mu.Unlock()
		cancel()
		return Task{}, errors.New("workbench: a task is already running")
	}
	w.active = r
	w.turns++
	followUp := w.turns > 1
	w.mu.Unlock()
	r.followUp = followUp

	// Record the working tree so the run's file changes can be reviewed. A
	// non-repository is not an error; there is simply nothing to compare.
	var review *changes.Snapshot
	if changes.IsRepo(ctx, w.workDir) {
		if snap, snapErr := changes.Take(ctx, w.workDir); snapErr == nil {
			review = snap
		}
	}
	w.mu.Lock()
	w.review = review
	w.mu.Unlock()

	w.emit(EventRunStarted, RunEvent{RunID: r.id, Prompt: prompt})

	go func() {
		defer cancel()
		var err error
		if routed {
			err = w.executeRouted(ctx, r, lead)
		} else {
			card := w.addCard(r.id, lead.ID, prompt)
			_, err = w.executeStep(ctx, r, lead, PhaseWork, prompt, card)
		}
		w.finishRun(ctx, r, err)
	}()

	return Task{ID: r.id, Prompt: prompt, AgentID: lead.ID, Routed: routed}, nil
}

// Chat asks the coordinator a question on the side channel.
//
// This is the path that stays open while specialists work. It runs concurrently
// with the active run and shares none of its state: no card is added, no
// snapshot is taken, no agent is seated, and the coordinator answers in a
// session of his own so two Claude processes never resume the same one.
//
// One question is answered at a time, for the same reason one run is: a second
// concurrent answer would interleave into the first. A question asked with
// nothing running is still a question, not a run -- the caller decides which
// it wants, and Submit remains the way to start work.
func (w *Workbench) Chat(prompt string) (Task, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return Task{}, errors.New("workbench: empty prompt")
	}
	if _, err := w.runner.Available(); err != nil {
		return Task{}, fmt.Errorf("workbench: local claude CLI not found on PATH: %w", err)
	}

	w.chatAdmit.Lock()
	defer w.chatAdmit.Unlock()

	lead, ok := w.registry.Coordinator()
	if !ok {
		return Task{}, errors.New("workbench: no coordinator configured")
	}

	ctx, cancel := context.WithCancel(context.Background())
	r := &run{
		id:      "c" + strconv.FormatUint(w.nextChat.Add(1), 10),
		prompt:  prompt,
		cancel:  cancel,
		agentID: lead.ID,
		chat:    true,
	}

	w.mu.Lock()
	if w.chat != nil {
		w.mu.Unlock()
		cancel()
		return Task{}, errors.New("workbench: still answering the last question")
	}
	w.chat = r
	w.chatTurns++
	r.followUp = w.chatTurns > 1
	// What the run is working on, read while the lock is held so the answer
	// describes the run that was live when the question was asked.
	var about string
	if w.active != nil {
		about = w.active.prompt
	}
	w.mu.Unlock()

	w.emit(EventChatStarted, RunEvent{RunID: r.id, AgentID: lead.ID, Prompt: prompt})

	go func() {
		defer cancel()
		err := w.answer(ctx, r, lead, about)

		w.mu.Lock()
		if w.chat == r {
			w.chat = nil
		}
		w.mu.Unlock()

		switch {
		case errors.Is(err, context.Canceled) || ctx.Err() != nil:
			w.emit(EventChatFinished, RunEvent{RunID: r.id, AgentID: lead.ID, Cancelled: true})
		case err != nil:
			w.emit(EventChatFinished, RunEvent{RunID: r.id, AgentID: lead.ID, Message: err.Error()})
		default:
			w.emit(EventChatFinished, RunEvent{RunID: r.id, AgentID: lead.ID})
		}
	}()

	return Task{ID: r.id, Prompt: prompt, AgentID: lead.ID}, nil
}

// answer is one side-channel turn, with the same bad-session retry executeStep
// uses and none of its bookkeeping.
func (w *Workbench) answer(ctx context.Context, r *run, lead agents.Agent, about string) error {
	key := chatSession(lead.ID)
	taskID := r.taskID(lead.ID)
	prompt := chatPrompt(r.prompt, about)

	resume := w.sessionFor(key)
	_, produced, err := w.streamChat(ctx, r, lead, taskID, prompt, resume)
	if err != nil && resume != "" && !produced && ctx.Err() == nil {
		w.forgetSession(key)
		_, _, err = w.streamChat(ctx, r, lead, taskID, prompt, "")
	}
	return err
}

// streamChat runs the coordinator's side-channel turn, recording its session
// under the chat key so it never collides with the run's.
func (w *Workbench) streamChat(
	ctx context.Context,
	r *run,
	lead agents.Agent,
	taskID string,
	prompt string,
	resume string,
) (string, bool, error) {
	return w.streamStep(ctx, r, lead, PhaseChat, taskID, prompt, resume)
}

// chatPrompt tells the coordinator what he is being asked about.
//
// Without this he would be answering "how is it going" with no idea what "it"
// is: his side-channel session has never seen the run's prompt.
func chatPrompt(question, about string) string {
	if about == "" {
		return question
	}
	return "The user is asking you something while work is already in flight. " +
		"The request being worked on right now is:\n\n" + about +
		"\n\nYou are on a side channel: answer the question, and do not start " +
		"or reassign work. If the answer is that the work should change course, " +
		"say so and let the user stop the run.\n\nTheir question:\n\n" + question
}

// Board returns the current task board.
func (w *Workbench) Board() []board.Card {
	return w.board.Snapshot()
}

// ClearConversation forgets every agent's session and empties the board, so the
// next request starts a new conversation rather than continuing this one.
func (w *Workbench) ClearConversation() {
	w.mu.Lock()
	w.sessions = make(map[sessionKey]string)
	w.turns = 0
	w.chatTurns = 0
	w.mu.Unlock()

	w.board.Clear()
	w.publishBoard()
}

// applyUpdates performs Anton's board edits. The plan has already been
// normalised, so every update names a card that exists and carries a change.
func (w *Workbench) applyUpdates(updates []BoardUpdate) {
	if len(updates) == 0 {
		return
	}
	for _, u := range updates {
		w.board.Update(u.TaskID, u.Title, u.AgentID, u.Status)
	}
	w.publishBoard()
}

// addCard puts a task on the board and publishes the change.
func (w *Workbench) addCard(runID, agentID, title string) string {
	id := w.board.Add(runID, agentID, title)
	w.publishBoard()
	return id
}

// moveCard changes a card's column and publishes the change. An empty cardID is
// ignored, so callers need not special-case work that has no card.
func (w *Workbench) moveCard(cardID string, status board.Status, note string) {
	if cardID == "" {
		return
	}
	w.board.SetStatus(cardID, status, note)
	w.publishBoard()
}

func (w *Workbench) publishBoard() {
	w.emit(EventBoardUpdated, BoardEvent{Cards: w.board.Snapshot()})
}

// Cancel stops the run with the given ID. Cancelling an unknown or already
// finished run is not an error: the caller's intent is already satisfied.
func (w *Workbench) Cancel(runID string) error {
	w.mu.Lock()
	active := w.active
	chat := w.chat
	w.mu.Unlock()
	if active != nil && active.id == runID {
		active.cancel()
	}
	// A side-channel answer is stoppable on its own, and only on its own:
	// stopping a question must never stop the work it was asking about.
	if chat != nil && chat.id == runID {
		chat.cancel()
	}
	return nil
}

// executeRouted runs the full coordinator flow: plan, delegate, synthesise.
//
// A one-agent office -- the state right after setup, before any specialist
// folder exists -- has nobody to route to, so the planning turn is skipped
// and the coordinator answers directly rather than failing the task.
func (w *Workbench) executeRouted(ctx context.Context, r *run, lead agents.Agent) error {
	if len(specialistIDs(w.registry)) == 0 {
		card := w.addCard(r.id, lead.ID, r.prompt)
		_, err := w.executeStep(ctx, r, lead, PhaseWork, r.prompt, card)
		return err
	}

	plan, err := w.plan(ctx, r, lead)
	if err != nil {
		return err
	}
	// Anton's bookkeeping lands before any work starts, so the board already
	// reflects what he decided by the time you read his reasoning.
	w.applyUpdates(plan.Updates)

	w.emit(EventRunPlan, RunEvent{
		RunID:   r.id,
		AgentID: lead.ID,
		Mode:    plan.Mode,
		Reason:  plan.Reason,
		Steps:   plan.Steps,
		Updates: plan.Updates,
	})

	// Anton kept it: one ordinary turn on the user's own words.
	if plan.Mode == ModeSelf {
		card := w.addCard(r.id, lead.ID, r.prompt)
		_, err = w.executeStep(ctx, r, lead, PhaseWork, r.prompt, card)
		return err
	}

	results, err := w.delegate(ctx, r, plan)
	if err != nil {
		return err
	}

	card := w.addCard(r.id, lead.ID, "Bring the answers together")
	_, err = w.executeStep(
		ctx, r, lead, PhaseSynthesis, synthesisPrompt(r.prompt, results), card)
	return err
}

// plan asks the coordinator who should do the work.
//
// The planning turn is schema-constrained, so its output is an object rather
// than prose. Its text is deliberately not streamed to the console: the parsed
// plan is emitted instead, which is the part worth reading.
func (w *Workbench) plan(ctx context.Context, r *run, lead agents.Agent) (Plan, error) {
	schema, err := planSchema(w.registry)
	if err != nil {
		return Plan{}, err
	}

	model := lead.PlanModel
	if model == "" {
		model = lead.Model
	}

	taskID := r.taskID("plan")
	w.beginTask(r, lead.ID, taskID, PhasePlan)

	var structured json.RawMessage
	err = w.runner.Run(ctx, claude.Request{
		Prompt:             planPrompt(r.prompt, r.followUp, w.board.Snapshot()),
		Model:              model,
		AppendSystemPrompt: w.systemPrompt(lead),
		WorkDir:            w.workDir,
		JSONSchema:         schema,
		// Routing is a judgement call on the text of the task, not an
		// investigation, and it is not the turn that does the work -- so it
		// runs read-only whatever the workbench is set to. The CLI has no way
		// to say "no tools at all" (an empty --allowed-tools is the same as
		// omitting it), but this refuses every edit and every command before
		// it runs, which is the part that matters: the mode the user chose
		// applies to the agents who were given the task, not to the turn that
		// decided who they are.
		PermissionMode: string(claude.PermissionRead),
	}, func(e claude.Event) {
		switch e.Kind {
		case claude.KindSession:
			// Deliberately not remembered: the routing turn is
			// schema-constrained and stateless, and adopting its session would
			// contaminate the conversation Anton actually answers in.
			w.emitClaude(EventClaudeSession, r, lead.ID, taskID, PhasePlan, ClaudeEvent{
				SessionID: e.SessionID, Model: e.Model,
			})
		case claude.KindResult:
			structured = e.Structured
			w.emitClaude(EventClaudeResult, r, lead.ID, taskID, PhasePlan, ClaudeEvent{
				CostUSD: e.CostUSD, DurationMS: e.DurationMS,
			})
		case claude.KindError:
			w.emitClaude(EventClaudeError, r, lead.ID, taskID, PhasePlan, ClaudeEvent{
				Message: e.Message,
			})
		}
	})

	w.endTask(r, lead.ID, taskID, PhasePlan, err)
	if err != nil {
		return Plan{}, err
	}
	if len(structured) == 0 {
		return Plan{}, errors.New("workbench: planning turn returned no plan")
	}

	var plan Plan
	if err := json.Unmarshal(structured, &plan); err != nil {
		return Plan{}, fmt.Errorf("workbench: parse plan: %w", err)
	}
	plan.normalise(w.registry, w.board.Has)
	return plan, nil
}

// delegate runs every step of a plan at the same time and collects the answers.
//
// "At the same time" is bounded by file ownership. Steps whose files do not
// overlap run together, as they always have. A step whose files are already
// taken sits on the board as blocked, naming who has them, and starts the
// moment they come free -- so an overlap Anton left in the plan costs the run
// time rather than losing somebody's edit.
//
// A specialist failing does not fail the run: its error is recorded and handed
// to synthesis, so a partial answer still reaches the user. Only cancellation
// stops the whole run.
func (w *Workbench) delegate(ctx context.Context, r *run, plan Plan) ([]stepResult, error) {
	results := make([]stepResult, len(plan.Steps))

	// Every card goes up before any work starts, so the board shows the whole
	// assignment at once rather than appearing a task at a time.
	cards := make([]string, len(plan.Steps))
	for i, step := range plan.Steps {
		if _, ok := w.registry.Get(step.AgentID); !ok {
			continue
		}
		if step.TaskID != "" && w.board.Has(step.TaskID) {
			// Continuing a task already on the board rather than opening a
			// duplicate. normalise has already dropped invented IDs.
			w.board.Update(step.TaskID, "", step.AgentID, "")
			w.publishBoard()
			cards[i] = step.TaskID
			continue
		}
		cards[i] = w.addCard(r.id, step.AgentID, step.Task)
	}

	held := newClaims(plan.Steps)

	var wg sync.WaitGroup
	for i, step := range plan.Steps {
		agent, ok := w.registry.Get(step.AgentID)
		if !ok {
			continue
		}
		results[i] = stepResult{AgentID: agent.ID, AgentName: agent.Name, Task: step.Task}

		wg.Add(1)
		go func(i int, agent agents.Agent, step PlanStep, card string) {
			defer wg.Done()
			w.runStep(ctx, r, agent, step, card, held, &results[i])
		}(i, agent, step, cards[i])
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// runStep is one specialist's share of a plan, from queueing for its files to
// the answer it hands back.
//
// Two things can hold a step up, and they are not the same thing. Before it
// starts, its files may already be taken -- Anton left an overlap in the plan,
// and the step simply queues. After it starts, the specialist may find itself
// needing a file it was not given, and report that; then the step gives its own
// files back, waits for the colleague, and is handed the task again. Both show
// on the board as blocked, with a note naming who is in the way.
func (w *Workbench) runStep(
	ctx context.Context,
	r *run,
	agent agents.Agent,
	step PlanStep,
	cardID string,
	held *claims,
	out *stepResult,
) {
	// finish rather than release: the step is over, so anybody queueing on the
	// files this step declared can stop waiting for them even if it never
	// managed to take them.
	defer held.finish(agent.ID)

	waited, ok := w.awaitFiles(ctx, agent.ID, step.Files, cardID, held)
	if !ok {
		return
	}
	out.Waited = waited

	prompt := stepPrompt(step.Task, step.Files, held.heldByOthers(agent.ID))
	for attempt := 0; ; attempt++ {
		output, err := w.executeStep(ctx, r, agent, PhaseWork, prompt, cardID)
		out.Output = output
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				out.Err = err.Error()
			}
			return
		}

		// A clean answer with no marker is the ordinary case.
		paths := blockedPaths(output)
		if len(paths) == 0 {
			return
		}

		out.BlockedOn = paths
		blocker := held.pending(agent.ID, paths)
		if blocker != "" {
			out.BlockedBy = blocker
		}
		exhausted := attempt >= maxBlockedRetries
		// Nobody is in the files it named, and it has already had a run since
		// it first said so, which makes this a misused marker rather than a
		// queue. Another attempt would produce the same reply.
		nothingToWaitFor := blocker == "" && attempt > 0
		if exhausted || nothingToWaitFor {
			w.moveCard(cardID, board.StatusBlocked, blockedNote(blocker, paths))
			return
		}

		// Give up our own files first. A step that held while it waited could
		// be the thing its colleague is waiting for.
		held.wait(agent.ID)
		w.moveCard(cardID, board.StatusBlocked, blockedNote(blocker, paths))

		// Wait for the reported files as well as our own, then take both: the
		// second attempt needs to own what it stopped for.
		want := append(append([]string{}, step.Files...), paths...)
		if _, ok := w.awaitFiles(ctx, agent.ID, want, cardID, held); !ok {
			return
		}
		out.Retried = true
		prompt = retryPrompt(step.Task, want, paths)
	}
}

// awaitFiles queues until every path is the agent's, and reports who it queued
// behind. ok is false only when the run was cancelled while waiting.
//
// The card is moved to blocked while the step waits, so the reason a
// specialist is standing still is on the board rather than nowhere.
func (w *Workbench) awaitFiles(
	ctx context.Context,
	agentID string,
	paths []string,
	cardID string,
	held *claims,
) (waited string, ok bool) {
	for {
		// Take the waiter before testing, or a release between the two is
		// missed and the step waits for one that has already happened.
		next := held.waiter()
		blocker, got := held.take(agentID, paths)
		if got {
			return waited, true
		}
		if waited == "" {
			waited = blocker
			w.moveCard(cardID, board.StatusBlocked, blockedNote(blocker, paths))
		}
		select {
		case <-next:
		case <-ctx.Done():
			return waited, false
		}
	}
}

// blockedNote is the line the board carries while a step is held up. It names
// the colleague when there is one, because "blocked" without a who is not
// something the user can act on.
func blockedNote(blocker string, paths []string) string {
	files := strings.Join(paths, ", ")
	if blocker == "" {
		return "waiting on " + files
	}
	return "waiting on " + blocker + " to finish with " + files
}

// executeStep runs one Claude call as one agent and returns its final answer.
//
// Text streams to the console as it arrives, including for specialists, so a
// delegated run can be watched rather than waited for. Only the planning turn
// stays quiet, and it does not come through here.
//
// The turn continues the agent's session from earlier in the conversation when
// there is one. If resuming fails before producing anything -- a session the
// CLI no longer has, most likely -- the turn is retried once from scratch, so a
// stale session degrades into a fresh answer rather than a failed run.
func (w *Workbench) executeStep(
	ctx context.Context,
	r *run,
	agent agents.Agent,
	phase Phase,
	prompt string,
	cardID string,
) (string, error) {
	taskID := r.taskID(agent.ID)
	w.beginTask(r, agent.ID, taskID, phase)
	w.moveCard(cardID, board.StatusDoing, "")

	resume := w.sessionFor(runSession(agent.ID))
	out, produced, err := w.streamStep(ctx, r, agent, phase, taskID, prompt, resume)

	if err != nil && resume != "" && !produced && ctx.Err() == nil {
		w.forgetSession(runSession(agent.ID))
		out, _, err = w.streamStep(ctx, r, agent, phase, taskID, prompt, "")
	}

	w.endTask(r, agent.ID, taskID, phase, err)

	switch {
	case errors.Is(err, context.Canceled):
		// Stopped, not failed: the task is simply not done.
		w.moveCard(cardID, board.StatusTodo, "")
	case err != nil:
		w.moveCard(cardID, board.StatusBlocked, err.Error())
	default:
		w.moveCard(cardID, board.StatusDone, "")
	}

	return out, err
}

// streamStep is one attempt at one turn. produced reports whether anything
// reached the frontend, which decides whether a retry is safe.
func (w *Workbench) streamStep(
	ctx context.Context,
	r *run,
	agent agents.Agent,
	phase Phase,
	taskID string,
	prompt string,
	resume string,
) (out string, produced bool, err error) {
	var text strings.Builder
	working := false

	// A side-channel turn continues its own conversation, not the agent's, so
	// two live Claude processes never resume the same session.
	key := runSession(agent.ID)
	if r.chat {
		key = chatSession(agent.ID)
	}

	err = w.runner.Run(ctx, claude.Request{
		Prompt:             prompt,
		Model:              agent.Model,
		AppendSystemPrompt: w.systemPrompt(agent),
		WorkDir:            w.workDir,
		AllowedTools:       agent.AllowedTools,
		PermissionMode:     w.permissionFor(agent),
		Resume:             resume,
	}, func(e claude.Event) {
		// The first sign of real output promotes the agent from assigned to
		// working. The frontend uses this to seat them at their desk. A
		// side-channel turn skips it: the office is showing what the run is
		// doing, and answering a question is not a change to that.
		if !working && e.Kind != claude.KindError && !r.chat {
			working = true
			w.setState(agent.ID, StateWorking)
			w.emitAgent(EventAgentWorking, r, agent.ID, taskID, phase, StateWorking, "")
		}

		switch e.Kind {
		case claude.KindSession:
			w.rememberSession(key, e.SessionID)
			w.emitClaude(EventClaudeSession, r, agent.ID, taskID, phase, ClaudeEvent{
				SessionID: e.SessionID, Model: e.Model,
			})
		case claude.KindText:
			produced = true
			w.emitClaude(EventClaudeText, r, agent.ID, taskID, phase, ClaudeEvent{Text: e.Text})
		case claude.KindThinking:
			w.emitClaude(EventClaudeThinking, r, agent.ID, taskID, phase, ClaudeEvent{Text: e.Text})
		case claude.KindToolUse:
			produced = true
			w.emitClaude(EventClaudeTool, r, agent.ID, taskID, phase, ClaudeEvent{
				ToolID:    e.ToolID,
				ToolName:  e.ToolName,
				ToolInput: string(e.ToolInput),
			})
		case claude.KindToolResult:
			produced = true
			w.emitClaude(EventClaudeToolResult, r, agent.ID, taskID, phase, ClaudeEvent{
				ToolID:     e.ToolID,
				ToolResult: e.ToolResult,
				ToolFailed: e.ToolFailed,
			})
		case claude.KindResult:
			produced = true
			text.WriteString(e.Result)
			w.emitClaude(EventClaudeResult, r, agent.ID, taskID, phase, ClaudeEvent{
				Text: e.Result, CostUSD: e.CostUSD, DurationMS: e.DurationMS,
			})
		case claude.KindError:
			produced = true
			w.emitClaude(EventClaudeError, r, agent.ID, taskID, phase, ClaudeEvent{Message: e.Message})
		}
	})

	return text.String(), produced, err
}

// beginTask marks an agent as assigned and tells the office to walk them over.
func (w *Workbench) beginTask(r *run, agentID, taskID string, phase Phase) {
	w.mu.Lock()
	w.state[agentID] = StateAssigned
	w.current[agentID] = taskID
	w.mu.Unlock()
	w.emitAgent(EventAgentAssigned, r, agentID, taskID, phase, StateAssigned, "")
}

// endTask reports how a single agent's turn ended and settles their state.
func (w *Workbench) endTask(r *run, agentID, taskID string, phase Phase, err error) {
	w.mu.Lock()
	delete(w.current, agentID)
	w.mu.Unlock()

	switch {
	case errors.Is(err, context.Canceled):
		w.emitAgent(EventAgentFinished, r, agentID, taskID, phase, StateIdle, cancelledMessage)
	case err != nil:
		w.emitAgent(EventAgentError, r, agentID, taskID, phase, StateError, err.Error())
	default:
		w.emitAgent(EventAgentFinished, r, agentID, taskID, phase, StateFinished, "")
	}

	// The stored state settles back to idle regardless of outcome: "finished"
	// and "error" are transitions the frontend animates, not resting states.
	w.setState(agentID, StateIdle)
}

// finishRun closes out a run and releases the workbench for the next one.
func (w *Workbench) finishRun(ctx context.Context, r *run, err error) {
	w.mu.Lock()
	if w.active == r {
		w.active = nil
	}
	review := w.review
	w.mu.Unlock()

	w.publishChanges(r.id, review)

	cancelled := errors.Is(err, context.Canceled) || ctx.Err() != nil
	switch {
	case cancelled:
		w.emit(EventClaudeCancelled, ClaudeEvent{RunID: r.id})
		w.emit(EventRunFinished, RunEvent{RunID: r.id, Cancelled: true})
	case err != nil:
		w.emit(EventRunFinished, RunEvent{RunID: r.id, Message: err.Error()})
	default:
		w.emit(EventRunFinished, RunEvent{RunID: r.id})
	}
}

// ConfigRoot returns the folder agents are loaded from, or empty if the user
// has not picked one yet. The frontend uses the empty case to decide that this
// is a first run.
func (w *Workbench) ConfigRoot() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.root
}

// PermissionMode returns what agents are currently allowed to do.
func (w *Workbench) PermissionMode() claude.PermissionMode {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.permission
}

// SetPermissionMode changes what agents are allowed to do and remembers it.
//
// It takes effect on the next Claude process rather than on one already
// running: a mode is an argument to a process that has already started, so
// tightening this mid-run does not reach back into the turn in flight.
//
// The mode is applied before it is persisted, and a config file that cannot be
// written is reported without undoing the change. A user whose config folder
// is read-only should still be able to work in the mode they picked; what they
// lose is having it remembered, and that is what the error says.
func (w *Workbench) SetPermissionMode(mode string) (claude.PermissionMode, error) {
	parsed, err := claude.ParsePermissionMode(mode)
	if err != nil {
		return w.PermissionMode(), err
	}
	w.UsePermissionMode(parsed)
	if err := config.Update(func(c *config.Config) {
		c.PermissionMode = string(parsed)
	}); err != nil {
		return parsed, err
	}
	return parsed, nil
}

// UsePermissionMode applies a mode without persisting it. Startup uses it for
// the mode already in the config file, which does not need writing back.
func (w *Workbench) UsePermissionMode(mode claude.PermissionMode) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.permission = mode
}

// permissionFor is the mode one dispatch runs under.
//
// An agent's own definition wins when it sets one, so a specialist can be kept
// narrower than the workbench; nothing on disk sets it yet, which is why the
// toggle is the answer for everybody in practice.
func (w *Workbench) permissionFor(a agents.Agent) string {
	if a.PermissionMode != "" {
		return a.PermissionMode
	}
	return string(w.PermissionMode())
}

// SetConfigRoot points Work at a config folder, loads the team from it and
// remembers the choice for next time.
//
// The team is loaded before the choice is persisted, so a folder Work cannot
// use is reported rather than saved.
func (w *Workbench) SetConfigRoot(root string) ([]AgentStatus, error) {
	list, err := w.UseConfigRoot(root)
	if err != nil {
		return nil, err
	}
	// Read-modify-write rather than Save: the file holds the permission mode
	// too, and writing a fresh Config here would drop it.
	if err := config.Update(func(c *config.Config) { c.Root = root }); err != nil {
		return nil, err
	}
	return list, nil
}

// UseConfigRoot loads the team from a config folder without persisting the
// choice. Startup uses it to apply the folder already saved in the config.
func (w *Workbench) UseConfigRoot(root string) ([]AgentStatus, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("workbench: empty config folder")
	}
	return w.loadAgents(root)
}

// AgentProfile reads what one agent's folder currently says about them: the
// skills in skills/, their PERSONALITY.md and the line the coordinator routes
// them on.
//
// Read on demand rather than carried in AgentStatus. The roster goes to the
// frontend on every reload and every state change, a personality file may run
// to 32 KiB, and only the one panel that is open needs any of it.
func (w *Workbench) AgentProfile(id string) (agents.Profile, error) {
	a, ok := w.registry.Get(id)
	if !ok {
		return agents.Profile{}, fmt.Errorf("workbench: unknown agent %q", id)
	}
	return agents.ReadProfile(a)
}

// ReloadAgents rescans the config folder and rebuilds the team.
//
// This is the path for an agent added outside Work: a new folder under agents/
// becomes a colleague here, with no restart and nothing to fill in twice.
func (w *Workbench) ReloadAgents() ([]AgentStatus, error) {
	root := w.ConfigRoot()
	if root == "" {
		return nil, errors.New("workbench: no config folder selected")
	}
	return w.loadAgents(root)
}

// loadAgents repairs the folder layout, rescans it and swaps in the result.
//
// Reloading during a run is refused. Agents already dispatched hold their own
// copy of their definition, so a swap mid-run would not corrupt anything, but
// it would leave the board and the office describing a team that no longer
// matches the work in flight. The check is a guard against the user's own
// click, not against a concurrent caller; there is one window.
func (w *Workbench) loadAgents(root string) ([]AgentStatus, error) {
	w.mu.Lock()
	running := w.active != nil || w.chat != nil
	w.mu.Unlock()
	if running {
		return nil, errors.New("workbench: stop the run before reloading agents")
	}

	if err := agents.Ensure(root); err != nil {
		return nil, err
	}
	list, err := agents.Scan(root)
	if err != nil {
		return nil, err
	}
	w.registry.Replace(list)

	w.mu.Lock()
	w.root = root
	// Per-agent bookkeeping is reconciled rather than reset: an agent who
	// survived the rescan keeps the session they have been talking in, and one
	// whose folder is gone stops costing memory.
	state := make(map[string]AgentState, len(list))
	for _, a := range list {
		if s, ok := w.state[a.ID]; ok {
			state[a.ID] = s
		} else {
			state[a.ID] = StateIdle
		}
	}
	w.state = state
	for id := range w.current {
		if _, ok := state[id]; !ok {
			delete(w.current, id)
		}
	}
	for k := range w.sessions {
		if _, ok := state[k.agentID]; !ok {
			delete(w.sessions, k)
		}
	}
	w.mu.Unlock()

	return w.Agents(), nil
}

// systemPrompt is what an agent is told about themselves for one turn: Work's
// own definition of the role, the PERSONALITY.md in their folder as it reads
// right now, and -- for the coordinator -- who else works here.
//
// The file is read here rather than held in the registry so that editing it in
// an editor takes effect on the very next task. A file that cannot be read is
// not worth failing a task over; the built-in prompt still describes the agent.
//
// The roster goes here rather than into a single turn's text because the
// coordinator answers as himself on four different kinds of turn and is liable
// to be asked about his team on any of them. Only the routing turn used to
// carry it, so a question about the team asked anywhere else was answered from
// nothing at all.
func (w *Workbench) systemPrompt(a agents.Agent) string {
	parts := make([]string, 0, 3)
	if a.SystemPrompt != "" {
		parts = append(parts, a.SystemPrompt)
	}
	if personality, err := agents.ReadPersonality(a.Dir); err == nil && personality != "" {
		parts = append(parts, personality)
	}
	if a.Role == agents.RoleCoordinator {
		parts = append(parts, rosterBlock(w.registry))
	}
	return strings.Join(parts, "\n\n")
}

// sessionKey identifies one conversation an agent is holding.
//
// It is a struct rather than a decorated string because an agent ID is just a
// folder name: any separator a suffix could use is a legal thing to call a
// folder, so a suffix could be forged by naming one. There is nothing to
// collide with here.
type sessionKey struct {
	agentID string
	// chat marks the coordinator's side-channel conversation, which is kept
	// apart from the run's so the two never resume each other.
	chat bool
}

// runSession is the key for an agent's conversation inside runs.
func runSession(agentID string) sessionKey {
	return sessionKey{agentID: agentID}
}

// chatSession is the key for the coordinator's side-channel conversation.
func chatSession(agentID string) sessionKey {
	return sessionKey{agentID: agentID, chat: true}
}

// sessionFor returns the session held under key in this conversation, if any.
func (w *Workbench) sessionFor(key sessionKey) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sessions[key]
}

// rememberSession records the session a turn ran in, so the next turn can
// continue it.
func (w *Workbench) rememberSession(key sessionKey, sessionID string) {
	if sessionID == "" {
		return
	}
	w.mu.Lock()
	w.sessions[key] = sessionID
	w.mu.Unlock()
}

// forgetSession drops a session after it proves unusable, so the next attempt
// starts clean.
func (w *Workbench) forgetSession(key sessionKey) {
	w.mu.Lock()
	delete(w.sessions, key)
	w.mu.Unlock()
}

// publishChanges lists what the run did to the working tree.
//
// It runs on a fresh context: the run's own context is cancelled by the time a
// cancelled run reaches here, and a stopped run is exactly when you most want
// to see what it managed to change first.
func (w *Workbench) publishChanges(runID string, review *changes.Snapshot) {
	if review == nil {
		w.emit(EventRunChanges, ChangesEvent{RunID: runID, Tracked: false})
		return
	}
	list, err := review.Diff(context.Background())
	if err != nil {
		w.emit(EventRunChanges, ChangesEvent{RunID: runID, Tracked: false})
		return
	}
	w.emit(EventRunChanges, ChangesEvent{RunID: runID, Changes: list, Tracked: true})
}

// Revert undoes one file change from the last run, restoring the content the
// run started with.
func (w *Workbench) Revert(path string) error {
	w.mu.Lock()
	review := w.review
	active := w.active
	w.mu.Unlock()

	if active != nil {
		return errors.New("workbench: stop the run before reverting its changes")
	}
	if review == nil {
		return errors.New("workbench: no reviewable run")
	}
	if err := review.Revert(context.Background(), path); err != nil {
		return err
	}
	w.publishChanges("", review)
	return nil
}

// Changes lists what the last run did to the working tree.
func (w *Workbench) Changes() []changes.Change {
	w.mu.Lock()
	review := w.review
	w.mu.Unlock()

	if review == nil {
		return nil
	}
	list, err := review.Diff(context.Background())
	if err != nil {
		return nil
	}
	return list
}

func (w *Workbench) setState(agentID string, s AgentState) {
	w.mu.Lock()
	w.state[agentID] = s
	w.mu.Unlock()
}

func (w *Workbench) emitAgent(
	name string, r *run, agentID, taskID string, phase Phase, s AgentState, msg string,
) {
	w.emit(name, AgentEvent{
		AgentID: agentID,
		TaskID:  taskID,
		RunID:   r.id,
		Phase:   phase,
		State:   s,
		Message: msg,
	})
}

func (w *Workbench) emitClaude(
	name string, r *run, agentID, taskID string, phase Phase, e ClaudeEvent,
) {
	e.RunID = r.id
	e.TaskID = taskID
	e.AgentID = agentID
	e.Phase = phase
	w.emit(name, e)
}
