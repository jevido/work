import { Events } from "@wailsio/runtime";
import type { OfficeRenderer } from "../office/renderer";
import { AGENT_ASSIGNED, AGENT_ERROR, AGENT_FINISHED, AGENT_WORKING } from "./events";

/**
 * Connects backend agent events to the renderer.
 *
 * This is the whole animation contract: the backend says what happened, the
 * renderer decides how it looks. No coordinates cross the bridge, and no event
 * touches Svelte state, so a busy office costs no component updates.
 */
export function connectOffice(renderer: OfficeRenderer): () => void {
  const offs = [
    Events.On(AGENT_ASSIGNED, (e) => renderer.push(e.data.agentId, "walking")),
    Events.On(AGENT_WORKING, (e) => renderer.push(e.data.agentId, "working")),
    Events.On(AGENT_FINISHED, (e) => {
      // A cancelled task is not an achievement: send them straight back to
      // wandering instead of playing the finished flourish.
      const state = e.data.message === "cancelled" ? "idle" : "finished";
      renderer.push(e.data.agentId, state);
    }),
    Events.On(AGENT_ERROR, (e) => renderer.push(e.data.agentId, "error")),
  ];

  return () => {
    for (const off of offs) off();
  };
}
