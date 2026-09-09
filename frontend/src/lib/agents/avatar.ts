import type { AgentStatus } from "../../../bindings/dev.jevido/work/internal/workbench/models.js";

/**
 * The route the backend serves agent avatars on. An agent's picture is a file
 * in their folder, and a webview cannot open a path -- so the backend hands
 * out the bytes and this is where they are asked for. See avatars.go.
 */
const AVATAR_ROUTE = "/avatars/";

/**
 * Where to fetch an agent's picture, or null when they have none.
 *
 * `avatar` from the backend is a filesystem path. It is read here only as
 * "this agent has one": the path itself is unusable in a webview, and the
 * backend resolves the file from the id anyway.
 *
 * `revision` is how many times the team has been read, and it is in the URL on
 * purpose. An avatar can be replaced in place -- same folder, same filename --
 * so a URL built from the id alone would be a cache hit forever and Reload
 * config would show the old picture. Bumping it per read means every reload
 * asks again; the backend answers an unchanged file with a 304, so asking is
 * close to free.
 */
export function avatarUrl(
  agent: Pick<AgentStatus, "id" | "avatar">,
  revision: number,
): string | null {
  if (!agent.avatar) return null;
  return `${AVATAR_ROUTE}${encodeURIComponent(agent.id)}?v=${revision}`;
}
