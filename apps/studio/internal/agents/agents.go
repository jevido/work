// Package agents defines the specialist agents Work can put to work and the
// static configuration each one carries.
//
// Everything in this package is data, not behaviour. The fields that are not
// used yet (SystemPrompt, AllowedTools, Model) exist because the roadmap needs
// per-agent prompts, skills and tool access, and it is cheaper to carry the
// fields from the start than to reshape the registry later.
package agents

import (
	"strings"
	"sync"
)

// Role marks what an agent is for. Anton coordinates; the others specialise.
type Role string

const (
	RoleCoordinator Role = "coordinator"
	RoleSpecialist  Role = "specialist"
)

// Desk is the agent's fixed spot in the office, in world coordinates.
//
// The canvas renderer uses a fixed logical world (see frontend office/world.ts)
// so these numbers stay meaningful no matter how the window is resized. The
// layout is a classroom: the coordinator's desk is at the front, the
// specialists' desks face it from below.
type Desk struct {
	// X and Y are the centre of the desk surface.
	X float64 `json:"x"`
	Y float64 `json:"y"`
	// SeatX and SeatY are where the agent stands/sits when working. A seat is
	// always in front of its desk, so the desk reads as furniture the agent is
	// working at rather than something they are standing on.
	SeatX float64 `json:"seatX"`
	SeatY float64 `json:"seatY"`
}

// Agent is the static definition of a worker. Runtime state lives in
// internal/workbench, deliberately separate, so this can eventually be loaded
// from user configuration without touching the state machine.
type Agent struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Role  Role   `json:"role"`
	Title string `json:"title"`
	// Skillset is what this agent is good at, one skill per entry. The
	// coordinator's routing prompt is built from these, so they are the part
	// of an agent's identity that changes where work goes.
	Skillset []string `json:"skillset"`
	// Modes are the conversations this agent leads: "orientation", "isolation",
	// "implementation". Empty means it leads none, which is true of every agent the user
	// authors and of every specialist.
	//
	// A field rather than a third Role, because Role is read in five places
	// that each ask a binary question -- who gets a desk at the front, who may
	// be delegated to, who is named in the roster, who receives a task first --
	// and the answer at all five for a mode owner is "the same as anybody
	// else". Leading a conversation and being delegable are different
	// properties, and collapsing them into one enum means changing either one
	// moves the other.
	//
	// Not settable from disk, for the reason Role is not: Scan attaches the
	// built-in struct by folder name, so agents/jared inherits these and a
	// folder called anything else cannot quietly take a mode off the agent
	// that owns it. See Registry.For for what happens when nobody claims one.
	Modes []string `json:"modes,omitempty"`
	// Advisory marks an agent who thinks with somebody and never does the
	// work. They lead conversations, argue, organise and write things onto the
	// board; they are never given a step, never edit a file and never run a
	// command.
	//
	// A property rather than a line in a system prompt, because a prompt is a
	// request and this is a rule. "Do not write code" has always been in
	// Jared's prompt and it was never a guarantee: the coordinator could still
	// route him a step, at which point he was a specialist with the user's
	// permission mode and the full tool list, being asked to implement
	// something while his prompt told him not to. Whichever of the two won,
	// the product had lied about one of them.
	//
	// So it is enforced in the four places a run can reach an agent, each of
	// which is a different way in rather than a belt on the same braces:
	//
	//   - planSchema leaves them out of the enum of ids a routing turn may
	//     name, so the model cannot produce a step for one.
	//   - Plan.normalise drops a step naming one anyway, which covers a plan
	//     that arrived from somewhere other than this build's schema.
	//   - Workbench.streamStep hands them ReadOnlyTools rather than the
	//     agent's own list, so the tools that change things are not there.
	//   - Workbench.permissionFor pins them to one mode whatever the window is
	//     set to, so the user's toggle can neither widen them nor drop them
	//     into the CLI's plan-mode workflow, which is a way of working and not
	//     a permission.
	//
	// Not settable from disk, for the same reason Role and Modes are not: Scan
	// attaches the built-in struct by folder name, so nobody can take the
	// guardrail off an advisory agent by editing a file, and nobody can put
	// one on a specialist by accident.
	Advisory bool `json:"advisory,omitempty"`
	// Personality is a short description of how the agent behaves: the traits
	// that colour an answer without changing what it knows.
	Personality string `json:"personality,omitempty"`
	// Experience is a short backstory or seniority note.
	Experience string `json:"experience,omitempty"`
	Colour     string `json:"colour"`
	Desk       Desk   `json:"desk"`

	// Dir is the agent's folder under <root>/agents, empty for a built-in
	// agent with no folder behind it. PERSONALITY.md is read from here every
	// time the agent is given work, so editing it in an editor takes effect on
	// the next task rather than on the next restart.
	Dir string `json:"-"`
	// Avatar is the path to the agent's avatar image, if the folder has one.
	// It is a filesystem path, not a URL: what to do with it is the
	// frontend's problem, and an agent without one is normal.
	Avatar string `json:"avatar,omitempty"`
	// Summary is the opening line of PERSONALITY.md as of the last scan. It
	// stands in for Skillset in the coordinator's roster when a folder-defined
	// agent lists no skills, so a new folder is routable the moment it exists.
	Summary string `json:"summary,omitempty"`

	// Model is the Claude model alias this agent runs on. Empty means "let the
	// local Claude installation decide".
	Model string `json:"model"`
	// PlanModel is the model used for a coordinator's routing turn. Routing is
	// a short, schema-constrained judgement call, so it does not need the
	// agent's main model. Empty falls back to Model.
	PlanModel string `json:"planModel,omitempty"`
	// SystemPrompt is appended to Claude's own system prompt when this agent
	// runs. Not wired into delegation yet; Anton's is used for direct tasks.
	SystemPrompt string `json:"-"`
	// AllowedTools restricts which tools the agent may use. Empty means the
	// local Claude defaults apply.
	AllowedTools []string `json:"-"`
	// PermissionMode is the Claude permission mode this agent runs under.
	// Empty inherits the user's own configuration, so Work never grants an
	// agent more than the user has already allowed. Setting it to
	// "acceptEdits" lets the agent write files, with Work's change review as
	// the safety net.
	PermissionMode string `json:"permissionMode,omitempty"`
}

// Blurb is the one-line description of an agent used to route work to them.
//
// The skillset is the better signal and is what the built-in team carries, but
// an agent that arrived as a folder may only have a PERSONALITY.md; its first
// line is a truthful, cheap substitute.
func (a Agent) Blurb() string {
	if len(a.Skillset) > 0 {
		return strings.Join(a.Skillset, ", ")
	}
	return a.Summary
}

// Registry is an ordered set of agents, safe for concurrent use.
//
// An agent is a folder on disk, so nothing in here is edited in place: the
// only write is Replace, which swaps the whole roster after a rescan. A single
// mutex around two small copies is all the synchronisation that needs. Agents
// are handed out by value, so a caller can never observe a half-applied swap.
type Registry struct {
	mu      sync.RWMutex
	ordered []Agent
	byID    map[string]Agent
}

// NewRegistry builds a registry from the given agents, preserving order.
func NewRegistry(list ...Agent) *Registry {
	r := &Registry{
		ordered: make([]Agent, len(list)),
		byID:    make(map[string]Agent, len(list)),
	}
	copy(r.ordered, list)
	for _, a := range list {
		r.byID[a.ID] = a
	}
	return r
}

// All returns the agents in display order.
func (r *Registry) All() []Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Agent, len(r.ordered))
	copy(out, r.ordered)
	return out
}

// Get looks an agent up by ID.
func (r *Registry) Get(id string) (Agent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	a, ok := r.byID[id]
	return a, ok
}

// Replace swaps the whole roster, in display order.
//
// This is how a rescan of the agents folder lands: agents appear and disappear
// as folders do, so the registry cannot treat its contents as fixed. Callers
// that keep per-agent state of their own -- the workbench keeps two maps and a
// session per agent -- have to reconcile it against the new list themselves.
func (r *Registry) Replace(list []Agent) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.ordered = make([]Agent, len(list))
	copy(r.ordered, list)
	r.byID = make(map[string]Agent, len(list))
	for _, a := range list {
		r.byID[a.ID] = a
	}
}

// Coordinator returns the agent that receives tasks first.
func (r *Registry) Coordinator() (Agent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, a := range r.ordered {
		if a.Role == RoleCoordinator {
			return a, true
		}
	}
	return Agent{}, false
}

// Coordinator returns the agent that receives tasks first, and For returns
// the one that leads a conversation.
//
// Two lookups rather than one because they answer different questions. A task
// arrives with no mode and goes to whoever routes; a conversation arrives with
// a mode and goes to whoever owns that kind of thinking. Anton is the answer to
// the first in every mode there is, and to the second only in work.
func (r *Registry) For(mode string) (Agent, bool) {
	r.mu.RLock()
	for _, a := range r.ordered {
		for _, m := range a.Modes {
			if m == mode {
				r.mu.RUnlock()
				return a, true
			}
		}
	}
	r.mu.RUnlock()

	// Nobody claims it, which is the ordinary state of a roster somebody has
	// edited: deleting agents/jared must leave orientation mode working rather than
	// answering "no agent configured" to every question. The coordinator is
	// what this app had before modes had owners, so it is what it falls back
	// to.
	return r.Coordinator()
}

// Default is the team Work insists on: the two agents that own something no
// folder can be missing for. Everyone else is the user's to author -- a folder
// under agents/ is an agent, and agents/_template is there to be copied.
//
// Anton because Scan cannot assemble a team without a coordinator. Jared
// because orientation and isolation would otherwise be led by the agent whose whole
// prompt is about routing work, and an app whose thinking modes are run by the
// person who hands out tasks is the thing having two of them is for.
//
// Their roles, colours, models and modes are Work's to guarantee. Their
// personality files are the user's documents.
func Default() *Registry {
	return newPlacedRegistry(
		Agent{
			ID:     "anton",
			Name:   "Anton",
			Role:   RoleCoordinator,
			Title:  "Lead",
			Colour: "#f2b544",
			// Desk deliberately left to AssignDesks below: the office layout
			// has one source of truth, and a copy here goes stale the moment
			// the coordinator's row moves.
			// Every routed task pays for a planning turn, so it runs on a
			// cheaper, faster model than the work itself.
			PlanModel: "sonnet",
			// The two modes that run against a working tree. He is still the
			// agent every task is routed through whatever mode raised it --
			// routing has no mode -- but the orientation and isolation
			// conversations belong to Jared below, because those two reshape a
			// document and never touch a file.
			Modes: []string{"implementation", "documentation"},
			Skillset: []string{
				"task intake", "planning", "delegation", "synthesis",
			},
			// Deliberately names no colleagues. Every turn he takes is handed
			// the roster as it actually is, so a prompt that also listed a team
			// would be a second, staler answer to the same question -- and it
			// would invent absent specialists on a roster that had changed.
			SystemPrompt: "You are Anton, the coordinating engineer of the Work " +
				"workbench. You receive the task first. Every turn you take lists " +
				"the specialists who currently exist; read that roster and decide " +
				"whether to answer the task yourself or to split it between the " +
				"ones whose specialties it genuinely spans. That list is also the " +
				"answer when the user asks who works here: never name a colleague " +
				"who is not on it, never say the team is empty when it is not, " +
				"and do not delegate for the sake of it. Be direct and " +
				"concrete.\n\n" +
				"Your specialists share one working tree and they work at the " +
				"same time, so you own the files as well as the work. Split a " +
				"task by file, not only by topic: every step you hand out names " +
				"the paths it will edit, and no two steps name the same file, " +
				"directory, or overlapping glob. When the work will not cut along " +
				"file lines, give the whole of it to one specialist instead of " +
				"splitting it -- two agents in one file is worse than one agent " +
				"doing more.\n\n" +
				"A specialist that stops because a file it needs is held by a " +
				"colleague reports back blocked rather than working around it. " +
				"That is the system working, not a failure: watch for it, keep " +
				"the task, and hand it back to that specialist once the colleague " +
				"is finished with the files. Say plainly in your answer when a " +
				"share of the work waited, and when a share never got its second " +
				"run and is therefore unfinished.",
		},
		Agent{
			ID:     "jared",
			Name:   "Jared",
			Role:   RoleSpecialist,
			Title:  "Thinking partner",
			Colour: "#7aa2f7",
			// Skills, for the profile panel and for anybody wondering what he
			// is for. Deliberately *not* what the coordinator routes on any
			// more: he is Advisory, so he is not in the enum a routing turn
			// chooses from, and these terms are read by people rather than
			// matched against a task.
			Skillset: []string{
				"shaping an idea into something buildable",
				"asking what a thing is actually for",
				"breaking work into ordered tasks",
				"spotting what two ideas have in common",
				"writing a board of cards somebody else can read",
			},
			Modes:    []string{"orientation", "isolation"},
			Advisory: true,
			SystemPrompt: "You are Jared. Somebody is thinking out loud and you " +
				"are thinking with them, over a board of cards the two of you " +
				"are building together.\n\n" +
				"Be a partner, not an assistant. You have read the board, you " +
				"have an opinion about it, and you say it without being asked. " +
				"\"I think the second one is the real problem and the other " +
				"three are symptoms of it\" is the job. So is \"I do not know, " +
				"and here is what would tell us.\" Enthusiasm about an idea you " +
				"have been given no reason to believe in is the one thing that " +
				"makes you useless.\n\n" +
				"Write things down as you go, without asking permission for each " +
				"one. Nothing lands until it is approved, so the cost of putting " +
				"a card up is one click -- and a conversation that ends in " +
				"agreement and an unchanged board has produced nothing. Say in a " +
				"sentence what you added and why, then stop: a summary as long " +
				"as the board is a second board nobody asked for.\n\n" +
				"Push back. If two cards say the same thing, say so. If a card " +
				"is three ideas wearing one coat, split it. If somebody is about " +
				"to plan work for a problem they have not stated, ask what the " +
				"problem is before you help them solve it.\n\n" +
				"But pushing back is what you do *alongside* the work, not " +
				"instead of it. Asked to map a product, a domain or a system, " +
				"map it: build the board out of what that kind of thing is made " +
				"of, several levels deep, and say what you assumed. Somebody who " +
				"asked for the shape of their product and got four questions " +
				"about why they are building it has been given nothing they can " +
				"correct. Put the questions in your reply, under the board.\n\n" +
				"Ask one question at a time and answer it yourself where you " +
				"can: four questions in a row is an interrogation, and most of " +
				"them you could have guessed at.\n\n" +
				"Talk like somebody who has done this before. Short sentences. " +
				"No headings in a reply that is four lines long, no list of what " +
				"you are about to do, and no telling them what a good idea their " +
				"idea was.\n\n" +
				"In isolation mode the order is the content: a plan is a sequence " +
				"somebody can pick up cold, smallest genuinely-shippable step " +
				"first, each one small enough to finish. Say when a task is too " +
				"big rather than writing it down and hoping.\n\n" +
				"You are a sparring partner, and that is the whole of the job. " +
				"You do not write code, edit files, run commands, or start " +
				"work, and this is not a rule you are being asked to keep -- " +
				"the tools are not there. You read, you argue, and you write " +
				"onto the board. Work is Anton's, and he cannot hand you any: " +
				"you are not in the list he routes from.\n\n" +
				"So do not offer. \"I could implement that for you\" is an " +
				"offer you cannot keep, and somebody who accepts it has been " +
				"misled about what this conversation is. When the thinking is " +
				"done and the thing wants building, say so plainly and say it " +
				"is Anton's -- then stop. Half-writing it in the chat window " +
				"to be helpful is the same mistake in a smaller font: it is " +
				"work nobody can run, review or take back.\n\n" +
				"What you can always do is make the next step obvious. A board " +
				"somebody can hand to Anton without explaining it is the best " +
				"outcome of a conversation with you.",
		},
	)
}

// newPlacedRegistry builds a registry with desks assigned, so a team that
// never goes through Scan still sits where the office is drawn. Default() is
// exactly that team: it stands in before the user has picked a config root.
func newPlacedRegistry(list ...Agent) *Registry {
	AssignDesks(list)
	return NewRegistry(list...)
}

// ReadOnlyTools is what an advisory agent is offered: reading the project, and
// nothing else. See Agent.Advisory.
//
// No Bash, no Edit, no Write, no WebFetch. A thinking partner who can read the
// repository is more use than one who cannot, and every one of the others is a
// way to change something. This is the guardrail on its own: the permission
// mode an adviser runs under is chosen so that it never changes how he works,
// only what he could reach if this list were ever wrong.
//
// Named here rather than written at the call site so there is one answer to
// "what may an adviser touch". The CLI reads an empty list as "no restriction",
// so this is never allowed to become empty -- an adviser with no tool list is
// an adviser with every tool.
var ReadOnlyTools = []string{"Read", "Grep", "Glob"}
