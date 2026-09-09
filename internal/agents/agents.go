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

// Default is the team Work insists on, which is the coordinator and nobody
// else. Specialists are the user's to author: a folder under agents/ is an
// agent, and agents/_template is there to be copied into one.
//
// Anton is here rather than on disk because Scan cannot assemble a team
// without a coordinator, so his role, colour and planning model are Work's to
// guarantee. His personality file is still the user's document.
func Default() *Registry {
	return NewRegistry(
		Agent{
			ID:     "anton",
			Name:   "Anton",
			Role:   RoleCoordinator,
			Title:  "Lead",
			Colour: "#f2b544",
			Desk:   Desk{X: 500, Y: 140, SeatX: 500, SeatY: 208},
			// Every routed task pays for a planning turn, so it runs on a
			// cheaper, faster model than the work itself.
			PlanModel: "sonnet",
			Skillset: []string{
				"task intake", "planning", "delegation", "synthesis",
			},
			// Deliberately names no colleagues. The routing turn is handed the
			// roster as it actually is, so a prompt that also listed a team
			// would be a second, staler answer to the same question -- and it
			// would invent absent specialists on a roster that had changed.
			SystemPrompt: "You are Anton, the coordinating engineer of the Work " +
				"workbench. You receive the task first. Every routing turn lists " +
				"the specialists who currently exist; read that roster and decide " +
				"whether to answer the task yourself or to split it between the " +
				"ones whose specialties it genuinely spans. Never assume a " +
				"colleague who is not on the roster, and do not delegate for the " +
				"sake of it. Be direct and concrete.",
		},
	)
}
