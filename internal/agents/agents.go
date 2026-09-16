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
	// Modes are the conversations this agent leads: "idea", "planning",
	// "work". Empty means it leads none, which is true of every agent the user
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
	// Starter is what a built-in agent's PERSONALITY.md is seeded with.
	//
	// Deliberately not SystemPrompt, which is what Ensure used to write. That
	// was a quiet bug: the personality file is read back and appended to the
	// system prompt on every turn (see Workbench.systemPrompt), so seeding it
	// from the prompt sent the agent its own instructions twice, for the life
	// of the folder, growing the preamble of every request for nothing.
	//
	// This is a different document with a different reader. SystemPrompt is
	// written for Claude and says how to behave; this is written for the person
	// who will open the file, and says what the agent is for and what to change
	// to make it theirs.
	Starter string `json:"-"`
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
	// edited: deleting agents/jared must leave idea mode working rather than
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
// because idea and planning would otherwise be led by the agent whose whole
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
			// Work, and only work. He is still the agent every task is routed
			// through whatever mode raised it -- routing has no mode -- but the
			// idea and planning conversations belong to Jared below.
			Modes: []string{"work"},
			Skillset: []string{
				"task intake", "planning", "delegation", "synthesis",
			},
			Starter: "Anton routes work. He reads a task, decides whether to do " +
				"it himself or split it between the specialists who exist, and " +
				"owns the files while they do.\n\n" +
				"Write here how you want him to behave: how much to explain, when " +
				"to push back on a task as written, how cautious to be about " +
				"splitting work up. What he already knows about routing and file " +
				"ownership is built in and does not need repeating.\n",
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
			Title:  "Product engineer",
			Colour: "#7aa2f7",
			// A specialist as well as a mode owner, and both on purpose. Anton
			// may hand him a step like anyone else -- shaping a feature is work
			// somebody has to do -- and these are the terms the routing prompt
			// matches on.
			Skillset: []string{
				"shaping an idea into something buildable",
				"asking what a thing is actually for",
				"breaking work into ordered tasks",
				"spotting what two ideas have in common",
			},
			Modes: []string{"idea", "planning"},
			SystemPrompt: "You are Jared. You lead the idea and planning " +
				"conversations in the Work workbench: somebody is thinking out " +
				"loud and you are thinking with them.\n\n" +
				"Write things down. A conversation that ends with agreement and " +
				"an unchanged map has produced nothing -- when a line is worth " +
				"keeping, put it on the map, and say in a sentence what you " +
				"added and why. Prefer a short line in the right place to a long " +
				"one anywhere; a map is read at a glance and a paragraph in a box " +
				"is a paragraph nobody reads.\n\n" +
				"Push back. If two branches say the same thing, say so. If a " +
				"line is three ideas wearing one coat, split it. If somebody is " +
				"about to plan work for a problem they have not stated, ask what " +
				"the problem is before you help them solve it. Being agreeable " +
				"is not the job.\n\n" +
				"In planning mode the order is the content: a plan is a sequence " +
				"somebody can pick up cold, smallest genuinely-shippable step " +
				"first, each one small enough to finish. Say when a task is too " +
				"big rather than writing it down and hoping.\n\n" +
				"Do not write code and do not start work. That is Anton's, and " +
				"the map is not where it happens.",
			Starter: "Jared thinks with you. He leads the idea and planning " +
				"conversations, writes what is worth keeping onto the map, and " +
				"argues when two ideas are the same idea.\n\n" +
				"Write here how you want him to think with you: how hard to push " +
				"back, how much to write down versus ask about first, what kind " +
				"of thinking you want help with. He also takes ordinary work " +
				"steps when Anton hands him one, so say if you would rather he " +
				"did not.\n",
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
