/**
 * Everything the reader asks the server for.
 *
 * Four calls, all GET, all public. The write side of the API exists and is not
 * here on purpose: this bundle is served to anyone who opens the site, so a
 * publish call in it would be a publish call somebody could read the shape of.
 */

export type NavPage = {
  slug: string;
  title: string;
  description: string;
};

export type NavSpace = {
  slug: string;
  title: string;
  summary: string;
  pages: NavPage[];
};

export type Heading = {
  id: string;
  text: string;
  level: number;
};

export type Page = {
  space: string;
  slug: string;
  title: string;
  description: string;
  html: string;
  toc: Heading[];
  updatedAt: string;
};

export type Hit = {
  space: string;
  slug: string;
  title: string;
  excerpt: string;
  rank: number;
};

/** A failed request, with the server's own words when it had any. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
}

async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  const response = await fetch(path, { signal, headers: { accept: "application/json" } });
  if (!response.ok) {
    // The server answers errors as JSON with a message in them. When it has
    // not -- a proxy in the way, a network that answered HTML -- the status is
    // the only true thing available, so it is what gets shown.
    let message = `the server answered ${response.status}`;
    try {
      const body = await response.json();
      if (typeof body?.message === "string") message = body.message;
    } catch {
      /* not JSON; the status stands */
    }
    throw new ApiError(response.status, message);
  }
  return (await response.json()) as T;
}

export function nav(signal?: AbortSignal): Promise<{ spaces: NavSpace[] }> {
  return get("/v1/nav", signal);
}

export function page(space: string, slug: string, signal?: AbortSignal): Promise<Page> {
  return get(`/v1/pages/${encodeURIComponent(space)}/${slug.split("/").map(encodeURIComponent).join("/")}`, signal);
}

export function search(query: string, signal?: AbortSignal): Promise<{ hits: Hit[] }> {
  return get(`/v1/search?q=${encodeURIComponent(query)}`, signal);
}
