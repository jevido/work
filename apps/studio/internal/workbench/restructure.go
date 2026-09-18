package workbench

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"dev.jevido/work/apps/studio/internal/propose"
)

// proposalRun is what makes a run a restructuring rather than an answer.
//
// Held on the run rather than passed down, because streamStep is the one place
// a claude.Request is built and threading four more parameters through it for
// one caller would be worse than a field that is nil for every other run.
type proposalRun struct {
	// mcpConfig is the file the CLI is pointed at, which names this binary.
	mcpConfig string
	// state is the outline or the plan as it stands right now.
	state string
	// mode is [ModeOrientation] or [ModeIsolation].
	mode string
}

// Restructure asks Claude to propose changes to a tab's outline or plan.
//
// It returns when the run has been started, not when it has answered. The
// proposal never comes back through here: Claude calls the tool, the tool call
// goes past on the ordinary stream, and the review panel picks it up. See
// internal/propose for why the tool itself does nothing.
//
// Nothing in this path reaches the network. The document comes from the local
// replica and Claude runs against the user's own CLI, which is what makes a
// restructuring work with the server down, the key rejected, or no network at
// all -- the same promise every other edit in this app makes.
func (w *Workbench) Restructure(tabID, mode, request string) (Task, error) {
	request = strings.TrimSpace(request)
	if request == "" {
		return Task{}, errors.New("workbench: say what to change")
	}
	if mode != ModeOrientation && mode != ModeIsolation {
		return Task{}, fmt.Errorf("workbench: cannot restructure in %q", mode)
	}
	if _, err := w.runner.Available(); err != nil {
		return Task{}, fmt.Errorf("workbench: local claude CLI not found on PATH: %w", err)
	}

	// The mode's own agent. This is the call behind the chat panel in orientation and
	// isolation, and it is the whole reason those modes have an owner: the
	// person is thinking out loud at somebody, and who that somebody is
	// changes the answer more than any prompt here does.
	lead, ok := w.registry.For(mode)
	if !ok {
		return Task{}, errors.New("workbench: no agent configured")
	}

	// The state is read before anything is started, so what Claude is told is
	// what was true when the person asked -- not what it happens to be by the
	// time a subprocess has spawned.
	doc := w.WorkspaceDocument()
	if len(doc.Tree) == 0 {
		return Task{}, errors.New("workbench: this tab has no workspace to restructure")
	}
	state := StateBlock(doc, tabID, mode)

	// A temp directory of our own rather than a shared one: the config names
	// an executable the CLI will run, and it is removed when the run ends.
	dir, err := os.MkdirTemp("", "work-mcp-")
	if err != nil {
		return Task{}, fmt.Errorf("workbench: %w", err)
	}
	config, err := propose.WriteConfig(dir)
	if err != nil {
		os.RemoveAll(dir)
		return Task{}, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	r := &run{
		id:     "p" + strconv.FormatUint(w.nextChat.Add(1), 10),
		prompt: request,
		cancel: cancel,
		// A side channel, like Chat: it must not move an agent to their desk,
		// touch the board, or look like work is in flight.
		chat: true,
		// Its own conversation, the same one the mode's chat uses -- but a
		// restructuring never resumes, so this only decides which transcript it
		// is counted against.
		conv: Conversation{TabID: tabID, Mode: mode},
		proposal: &proposalRun{
			mcpConfig: config,
			state:     state,
			mode:      mode,
		},
	}

	w.mu.Lock()
	if w.chat != nil {
		w.mu.Unlock()
		cancel()
		os.RemoveAll(dir)
		return Task{}, errors.New("workbench: still answering the last question")
	}
	w.chat = r
	w.mu.Unlock()

	w.emit(EventChatStarted, RunEvent{RunID: r.id, AgentID: lead.ID, Prompt: request})

	go func() {
		defer cancel()
		defer os.RemoveAll(dir)

		// No resume, deliberately. A restructuring carries the document as it
		// is right now; continuing a session would leave an older state block
		// in the history, and the most recent thing a model saw is not
		// reliably the thing it reasons from. Every proposal is asked cold.
		_, _, err := w.streamStep(ctx, r, lead, PhaseChat, r.taskID(lead.ID), proposalPrompt(r.proposal, request), "")

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

	return Task{ID: r.id, Prompt: request, AgentID: lead.ID}, nil
}

// proposalPrompt is what the model is asked, with what is there attached.
//
// The instruction lives here and the rules of the answer live in the tool's own
// description -- see internal/propose. Two places on purpose: this says what is
// true right now, that says how to reply, and the second is read at the moment
// of replying whether or not this prompt is still in view.
func proposalPrompt(p *proposalRun, request string) string {
	var b strings.Builder

	switch p.mode {
	case ModeIsolation:
		b.WriteString("You are being asked to change a plan: an ordered list of tasks, " +
			"each one extracted from an idea in the outline underneath it.\n\n")
	default:
		b.WriteString("You are being asked to work on a board of ideas.\n\n" +
			"The board maps something -- usually a product or a system -- so that all of it " +
			"can be seen at once. A subject in the middle, four to seven clusters around it " +
			"for its capabilities, and the parts of each capability hanging off it. Depth is " +
			"what carries the meaning: three or four levels is normal for a real system, and " +
			"you keep going until a leaf is concrete -- a screen, a rule, a document, a job " +
			"that runs, something a person does. Links join lines the tree cannot, captions " +
			"say why, and glyphs make a head findable.\n\n" +
			"A card names a part, in the words the domain already uses for it: " +
			"\"Memberships\", \"Direct debit runs\", \"Match sheets\". Not a question, not " +
			"an opinion, not a sentence. Questions and opinions go in your reply, where " +
			"somebody can answer them -- a board full of \"why are we doing this\" is a board " +
			"nobody can build from.\n\n" +
			"If the request names a thing and asks for its ideas, features or shape, map it. " +
			"Build it out of what that kind of system is made of, put what you assumed into " +
			"the detail of the card it affects, and say in your reply which parts you were " +
			"least sure about. Do not answer with questions instead of a board.\n\n" +
			"A card is a title and a detail: the title is drawn on the board and read " +
			"at a glance, the detail is what somebody sees when they open the card. What the " +
			"part covers, what it has to handle, what you assumed -- all of that goes in the " +
			"detail, not in a title that needs a comma and a because.\n\n" +
			"Above the outline are the workspace's guidelines -- what it is trying to be -- " +
			"and the people and groups waiting on something. Put cards under the guidelines " +
			"they genuinely serve, and name somebody as interested only where you have been " +
			"told they are. You cannot add to either list: say so if a card needs a word " +
			"that is not there.\n\n" +
			"Most requests change part of it: add notes, move them, group them, link them. " +
			"A request that hands you raw unsorted ideas and asks for a board out of them is " +
			"the one that starts with replace -- and everything on the board now is set aside " +
			"rather than deleted, so the person can take it back.\n\n")
	}

	b.WriteString("Answer by calling " + propose.ToolName + " exactly once. " +
		"Do not edit any files, do not run anything, and do not describe the change in prose " +
		"instead of calling the tool -- prose cannot be reviewed a row at a time and cannot be applied.\n\n")

	// Said here as well as in the tool description. It is the constraint that
	// makes the whole thing checkable, and a proposal naming an id that was
	// never shown is refused whole rather than partly applied.
	b.WriteString("Name only the ids below. An id you cannot see here does not exist.\n\n")

	b.WriteString(p.state)

	b.WriteString("\n\nWhat you have been asked to do:\n\n")
	b.WriteString(request)
	return b.String()
}
