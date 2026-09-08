// Package agents defines the specialist agents Work can put to work and the
// static configuration each one carries.
//
// Everything in this package is data, not behaviour. The fields that are not
// used yet (SystemPrompt, AllowedTools, Model) exist because the roadmap needs
// per-agent prompts, skills and tool access, and it is cheaper to carry the
// fields from the start than to reshape the registry later.
package agents

// Role marks what an agent is for. Anton coordinates; the others specialise.
type Role string

const (
	RoleCoordinator Role = "coordinator"
	RoleSpecialist  Role = "specialist"
)

// Desk is the agent's fixed spot in the office, in world coordinates.
//
// The canvas renderer uses a fixed logical world (see frontend office/world.ts)
// so these numbers stay meaningful no matter how the window is resized.
type Desk struct {
	// X and Y are the centre of the desk surface.
	X float64 `json:"x"`
	Y float64 `json:"y"`
	// SeatX and SeatY are where the agent stands/sits when working.
	SeatX float64 `json:"seatX"`
	SeatY float64 `json:"seatY"`
}

// Agent is the static definition of a worker. Runtime state lives in
// internal/workbench, deliberately separate, so this can eventually be loaded
// from user configuration without touching the state machine.
type Agent struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Role        Role     `json:"role"`
	Title       string   `json:"title"`
	Specialties []string `json:"specialties"`
	Colour      string   `json:"colour"`
	Desk        Desk     `json:"desk"`

	// Model is the Claude model alias this agent runs on. Empty means "let the
	// local Claude installation decide".
	Model string `json:"model"`
	// SystemPrompt is appended to Claude's own system prompt when this agent
	// runs. Not wired into delegation yet; Anton's is used for direct tasks.
	SystemPrompt string `json:"-"`
	// AllowedTools restricts which tools the agent may use. Empty means the
	// local Claude defaults apply.
	AllowedTools []string `json:"-"`
}

// Registry is an ordered, immutable set of agents.
type Registry struct {
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
	out := make([]Agent, len(r.ordered))
	copy(out, r.ordered)
	return out
}

// Get looks an agent up by ID.
func (r *Registry) Get(id string) (Agent, bool) {
	a, ok := r.byID[id]
	return a, ok
}

// Coordinator returns the agent that receives tasks first.
func (r *Registry) Coordinator() (Agent, bool) {
	for _, a := range r.ordered {
		if a.Role == RoleCoordinator {
			return a, true
		}
	}
	return Agent{}, false
}

// Default is the built-in team: Anton coordinates, Jeff and Chris specialise.
func Default() *Registry {
	return NewRegistry(
		Agent{
			ID:     "anton",
			Name:   "Anton",
			Role:   RoleCoordinator,
			Title:  "Lead",
			Colour: "#f2b544",
			Desk:   Desk{X: 300, Y: 200, SeatX: 300, SeatY: 268},
			Specialties: []string{
				"task intake", "planning", "delegation", "synthesis",
			},
			SystemPrompt: "You are Anton, the coordinating engineer of the Work " +
				"workbench. You receive the task first. Decide whether to do it " +
				"yourself or to hand parts to Jeff (Go, infrastructure, " +
				"performance, concurrency, dependency restraint) or Chris (UX, " +
				"interaction design, Svelte frontend, information hierarchy). " +
				"Be direct and concrete.",
		},
		Agent{
			ID:     "jeff",
			Name:   "Jeff",
			Role:   RoleSpecialist,
			Title:  "Systems",
			Colour: "#5bc8a0",
			Desk:   Desk{X: 700, Y: 200, SeatX: 700, SeatY: 268},
			Specialties: []string{
				"Go", "infrastructure", "architecture", "performance",
				"memory", "CPU", "concurrency", "systems integration",
				"dependency restraint",
			},
			SystemPrompt: "You are Jeff. You own Go, infrastructure, " +
				"architecture, performance, memory and CPU cost, concurrency and " +
				"systems integration. You are sceptical of expensive or " +
				"complicated solutions and of new dependencies; say so and " +
				"propose the cheaper option.",
		},
		Agent{
			ID:     "chris",
			Name:   "Chris",
			Role:   RoleSpecialist,
			Title:  "Experience",
			Colour: "#7aa7ff",
			Desk:   Desk{X: 500, Y: 470, SeatX: 500, SeatY: 538},
			Specialties: []string{
				"UX", "usability", "interaction design", "Svelte",
				"information hierarchy", "visual clarity", "reducing friction",
			},
			SystemPrompt: "You are Chris. You own UX, usability, interaction " +
				"design, Svelte frontend work, information hierarchy and visual " +
				"clarity. You question solutions that technically work but feel " +
				"bad to use, and you say what to do instead.",
		},
	)
}
