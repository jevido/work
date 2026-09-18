/**
 * The address bar, in thirty lines.
 *
 * A page is `/<space>/<slug>`, and the slug can have slashes in it. That is the
 * whole route table, so a router library here would be more configuration than
 * code. The server sends index.html for every path that is not a file, which is
 * what makes a deep link work on a cold load.
 */

export type Route = {
  space: string;
  slug: string;
};

/** The route the address bar currently names, or null at the root. */
export function current(): Route | null {
  return parse(location.pathname);
}

export function parse(path: string): Route | null {
  const parts = path.replace(/^\/+|\/+$/g, "").split("/");
  if (parts.length < 2 || !parts[0] || !parts[1]) return null;
  return { space: parts[0], slug: parts.slice(1).join("/") };
}

export function href(route: Route): string {
  return `/${route.space}/${route.slug}`;
}

/**
 * Navigates without reloading, and tells the caller where it landed.
 *
 * Exported rather than wired into a click handler here: the one place that
 * knows a click was on an internal link is the component that rendered it, and
 * a global listener would have to guess.
 */
export function go(route: Route): void {
  history.pushState(null, "", href(route));
  dispatchEvent(new PopStateEvent("popstate"));
}
