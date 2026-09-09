import { Events } from "@wailsio/runtime";
import { AgentState, Phase } from "../../../bindings/dev.jevido/work/internal/workbench/models";
import type { VisualState } from "../office/agent";
import type { TaskPhase } from "../office/handoff";
import type { OfficeRenderer } from "../office/renderer";
import { AGENT_ASSIGNED, AGENT_ERROR, AGENT_FINISHED, AGENT_WORKING } from "./events";

/**
 * The backend's lifecycle state as a visual one.
 *
 * Only needed for the roster, which is read once at startup and is the only
 * place a state arrives as a value rather than as an event. Everything after
 * that comes through the handlers below.
 */
export function visualStateOf(state: AgentState): VisualState {
  switch (state) {
    case AgentState.StateAssigned:
      return "walking";
    case AgentState.StateWorking:
      return "working";
    case AgentState.StateFinished:
      return "finished";
    case AgentState.StateError:
      return "error";
    default:
      return "idle";
  }
}

/**
 * Which part of a run an event belongs to.
 *
 * The office only needs this to tell work somebody was given from a turn the
 * coordinator is taking himself -- the first is handed over in person, the
 * second is his own paperwork. The enum's values are already these strings, so
 * this is a narrowing rather than a mapping, and an unrecognised phase reads as
 * "no phase" and simply gets the plain walk to a desk.
 */
function phaseOf(phase: Phase | undefined): TaskPhase | undefined {
  switch (phase) {
    case Phase.PhasePlan:
      return "plan";
    case Phase.PhaseWork:
      return "work";
    case Phase.PhaseSynthesis:
      return "synthesis";
    case Phase.PhaseChat:
      return "chat";
    default:
      return undefined;
  }
}

/**
 * Connects backend agent events to the renderer.
 *
 * This is the whole animation contract: the backend says what happened, the
 * renderer decides how it looks. No coordinates cross the bridge, and no event
 * touches Svelte state, so a busy office costs no component updates.
 */
export function connectOffice(renderer: OfficeRenderer): () => void {
  const offs = [
    Events.On(AGENT_ASSIGNED, (e) =>
      renderer.push(e.data.agentId, "walking", phaseOf(e.data.phase)),
    ),
    Events.On(AGENT_WORKING, (e) =>
      renderer.push(e.data.agentId, "working", phaseOf(e.data.phase)),
    ),
    Events.On(AGENT_FINISHED, (e) => {
      // A cancelled task is not an achievement: send them straight back to
      // wandering instead of playing the finished flourish.
      const state = e.data.message === "cancelled" ? "idle" : "finished";
      renderer.push(e.data.agentId, state, phaseOf(e.data.phase));
    }),
    Events.On(AGENT_ERROR, (e) =>
      renderer.push(e.data.agentId, "error", phaseOf(e.data.phase)),
    ),
  ];

  return () => {
    for (const off of offs) off();
  };
}
