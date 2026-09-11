/**
 * Every call the sync server takes, and the only place their URLs live.
 *
 * Written against the contract in server/README.md, which says the shapes are
 * fixed and that unknown response fields must be ignored -- so nothing here
 * checks for fields it does not use, and everything it does use has a fallback
 * for the field being absent.
 *
 *   POST /v1/workspaces                    Bearer <signup token>
 *   GET  /v1/workspace                     Bearer <read or write key>
 *   GET  /v1/ops?since=&limit=             Bearer <read or write key>
 *   POST /v1/ops                           Bearer <write key>
 *
 * There is no workspace id in any path: the key identifies the workspace.
 * There is no websocket and no SSE either -- live updates are polling, which
 * is why ./sync.svelte.ts is a loop rather than a subscription.
 */
import type { Op } from "./ops";

/**
 * Why a call did not work, in the flavours the UI treats differently.
 *
 * `rejected` is a fact about the key and will never fix itself; `unreachable`
 * is a fact about the network and usually will; `refused` is the server saying
 * this request was wrong, which retrying cannot help either but which is not
 * the user's key's fault. Collapsing the first two is the mistake this type
 * exists to prevent: a workspace whose key has been revoked would sit on
 * "offline" retrying forever, and the one action that fixes it -- paste a new
 * key -- is the one nothing would suggest.
 */
export type Failure = "unreachable" | "rejected" | "refused" | "rate-limited";

export class SyncError extends Error {
  readonly failure: Failure;
  /** The server's own `error.code`, when it sent one. For logs, not for logic. */
  readonly code: string | null;
  /** Seconds the server asked us to wait, from Retry-After. */
  readonly retryAfter: number | null;

  constructor(failure: Failure, message: string, code: string | null = null, retryAfter = 0) {
    super(message);
    this.name = "SyncError";
    this.failure = failure;
    this.code = code;
    this.retryAfter = retryAfter > 0 ? retryAfter : null;
  }
}

/** Workspace metadata, from GET /v1/workspace. */
export interface WorkspaceInfo {
  id: string;
  name: string;
  /** Highest sequence number in the log. A fresh workspace is 0. */
  head: number;
  /** What this key may do. The viewer shows nothing that writes when "read". */
  access: "read" | "write";
}

export interface Created {
  info: WorkspaceInfo;
  writeKey: string;
  readKey: string;
}

/** One page of the log. */
export interface Page {
  head: number;
  ops: Op[];
  /**
   * The sequence number of the last op on this page, which is the cursor for
   * the next call.
   *
   * Not `head`: a page is capped at 1000 ops and `head` is where the log ends,
   * so paging from `head` would skip everything between them. Sequence numbers
   * are gapless, so this is also exactly how far the caller has read.
   */
  lastSeq: number;
  /** True when there are ops after the last one here. Page again. */
  more: boolean;
}

export interface Accepted {
  head: number;
  /** Op ids the server had already, which the contract says is not an error. */
  duplicates: string[];
}

/**
 * What a workspace needs from a server.
 *
 * An interface rather than four loose functions, so a workspace with no server
 * -- which is every workspace until somebody shares one -- is a transport that
 * is simply absent rather than a flag every call site has to remember.
 */
export interface Transport {
  readonly base: string;
  info(): Promise<WorkspaceInfo>;
  /** One page from `since`. Paging is the caller's, because it decides when to stop. */
  since(seq: number): Promise<Page>;
  push(ops: readonly Op[]): Promise<Accepted>;
}

/** How long a call waits before the server counts as not being there. */
const TIMEOUT_MS = 10_000;

/** The contract's own ceiling. Sending more is a 400, not a bigger request. */
export const MAX_OPS_PER_PUSH = 500;

/**
 * The server to offer when nobody has said otherwise.
 *
 * Overridable at build time because there is no one right answer: this is
 * where the hosted one lives, and a developer runs their own next to the app.
 * It is prefilled into an editable field rather than assumed, so a wrong
 * default costs a paste rather than a failure nobody can locate.
 */
export const DEFAULT_SERVER: string =
  import.meta.env.VITE_WORK_SERVER ?? "https://work.jevido.app";

export function httpTransport(base: string, key: string): Transport {
  const root = base.replace(/\/+$/, "");

  return {
    base: root,

    async info() {
      const body = await call<{ workspace?: Partial<WorkspaceInfo>; access?: string }>(
        `${root}/v1/workspace`,
      );
      return {
        id: body.workspace?.id ?? "",
        name: body.workspace?.name ?? "",
        head: numberOr(body.workspace?.head, 0),
        access: body.access === "write" ? "write" : "read",
      };
    },

    async since(seq) {
      const body = await call<{ head?: number; ops?: { seq?: number; op?: Op }[]; more?: boolean }>(
        `${root}/v1/ops?since=${seq}`,
      );
      const entries = body.ops ?? [];
      let lastSeq = seq;
      for (const entry of entries) lastSeq = Math.max(lastSeq, numberOr(entry.seq, seq));
      return {
        head: numberOr(body.head, seq),
        ops: entries.map((e) => e.op).filter((op): op is Op => !!op),
        lastSeq,
        // A server that omitted `more` on a full page would have us stop
        // early, so a non-empty page is the fallback rather than false.
        more: body.more ?? entries.length > 0,
      };
    },

    async push(ops) {
      const body = await call<{
        head?: number;
        duplicates?: { id?: string }[];
      }>(`${root}/v1/ops`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ ops }),
      });
      return {
        head: numberOr(body.head, 0),
        duplicates: (body.duplicates ?? []).map((d) => d.id ?? "").filter((id) => id !== ""),
      };
    },
  };

  function call<T>(url: string, init: RequestInit = {}): Promise<T> {
    return request<T>(url, init, key);
  }
}

/**
 * Asks a server for a new workspace.
 *
 * Takes the server's signup token rather than a workspace key -- this is where
 * keys come from, so there is not one yet. A server with no token configured
 * answers 403, which the dialog reports as "this server is not handing out
 * workspaces" rather than as a bad token.
 */
export async function createWorkspace(
  base: string,
  signupToken: string,
  name: string,
): Promise<Created> {
  const root = base.replace(/\/+$/, "");
  const body = await request<{
    workspace?: Partial<WorkspaceInfo>;
    writeKey?: string;
    readKey?: string;
  }>(
    `${root}/v1/workspaces`,
    {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ name }),
    },
    signupToken,
  );

  if (!body.writeKey) {
    throw new SyncError("refused", "The server did not return a write key.");
  }
  return {
    info: {
      id: body.workspace?.id ?? "",
      name: body.workspace?.name ?? name,
      head: numberOr(body.workspace?.head, 0),
      access: "write",
    },
    writeKey: body.writeKey,
    readKey: body.readKey ?? "",
  };
}

/* -------------------------------------------------------------------------- */

async function request<T>(url: string, init: RequestInit, bearer: string): Promise<T> {
  const response = await fetchOrFail(url, {
    ...init,
    headers: { ...init.headers, authorization: `Bearer ${bearer}` },
  });

  if (!response.ok) throw await errorFor(response);

  try {
    return (await response.json()) as T;
  } catch {
    throw new SyncError("unreachable", "The server's answer was not readable.");
  }
}

/**
 * The server's error body, turned into something the badge can act on.
 *
 * Branched on `code`, which the contract fixes, and not on `message`, which it
 * says may change. The status is the fallback rather than the first choice:
 * a proxy in front of the server answers with its own body and no code at
 * all, and a 502 from nginx has to read as "cannot reach the server" the same
 * as a dropped connection does.
 */
async function errorFor(response: Response): Promise<SyncError> {
  let code: string | null = null;
  let message = "";
  try {
    const body = (await response.json()) as { error?: { code?: string; message?: string } };
    code = body.error?.code ?? null;
    message = body.error?.message ?? "";
  } catch {
    // Not JSON. The status is all there is.
  }

  const seconds = Number(response.headers.get("retry-after") ?? "");
  const retryAfter = Number.isFinite(seconds) ? seconds : 0;

  switch (code) {
    case "unauthorized":
    case "forbidden":
      return new SyncError("rejected", message || "The server would not accept this key.", code);
    case "rate_limited":
      return new SyncError(
        "rate-limited",
        message || "The server asked us to slow down.",
        code,
        retryAfter,
      );
    case "bad_request":
    case "too_large":
    case "method_not_allowed":
    case "not_found":
      return new SyncError("refused", message || "The server rejected the request.", code);
    case "internal":
      return new SyncError("unreachable", message || "The server had a problem.", code);
  }

  // No code, or one this version has not heard of.
  switch (response.status) {
    case 401:
    case 403:
      // 403 as well as 401: a read key on a write endpoint is authenticated
      // and not allowed, and from here those are the same problem -- this key
      // cannot do this, and waiting will not change it.
      return new SyncError("rejected", message || "The server would not accept this key.", code);
    case 429:
      return new SyncError(
        "rate-limited",
        message || "The server asked us to slow down.",
        code,
        retryAfter,
      );
    case 400:
    case 404:
    case 405:
    case 413:
      return new SyncError("refused", message || "The server rejected the request.", code);
    default:
      return new SyncError(
        "unreachable",
        message || `The server answered ${response.status}.`,
        code,
      );
  }
}

/**
 * fetch, with a timeout, and with a dead network as a SyncError.
 *
 * Without the timeout, a server that accepts the connection and then says
 * nothing leaves the badge on "syncing" forever -- the one state that promises
 * something is about to happen.
 */
async function fetchOrFail(url: string, init: RequestInit): Promise<Response> {
  const abort = new AbortController();
  const timer = setTimeout(() => abort.abort(), TIMEOUT_MS);
  try {
    return await fetch(url, { ...init, signal: abort.signal, cache: "no-store" });
  } catch {
    throw new SyncError("unreachable", "The server could not be reached.");
  } finally {
    clearTimeout(timer);
  }
}

function numberOr(value: unknown, fallback: number): number {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

/**
 * Pulls a key out of whatever somebody pasted.
 *
 * People share a link, not a key -- the viewer's own address is
 * `https://host/#k=rk_…`, and that is what lands on a clipboard. Asking
 * somebody to edit a URL down to its fragment before they can join is asking
 * them to do string surgery to open a document. So a full link, a link with
 * the key in the query, and a bare key all work, and when it was a link the
 * server it points at comes with it.
 */
export function parseInvite(input: string): { base: string | null; key: string } | null {
  const text = input.trim();
  if (text === "") return null;

  if (/^https?:\/\//i.test(text)) {
    let url: URL;
    try {
      url = new URL(text);
    } catch {
      return null;
    }
    const key = (
      new URLSearchParams(url.hash.replace(/^#/, "")).get("k") ??
      url.searchParams.get("k") ??
      ""
    ).trim();
    if (key === "") return null;
    return { base: url.origin, key };
  }

  // A bare key. Which server it belongs to is the form's own field.
  return { base: null, key: text };
}

/** True for a key the server would treat as a writer. Used to warn, never to block. */
export function looksLikeReadKey(key: string): boolean {
  return key.startsWith("rk_");
}
