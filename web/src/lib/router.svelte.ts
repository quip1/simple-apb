// SPDX-License-Identifier: AGPL-3.0-or-later
//
// A tiny History-API router. Routes are patterns like "/books/:id"; the
// server answers every non-API path with index.html, so deep links work.

export interface Route {
  path: string;
  params: Record<string, string>;
  query: URLSearchParams;
}

function current(): Route {
  return { path: location.pathname, params: {}, query: new URLSearchParams(location.search) };
}

export const route = $state<Route>(current());

window.addEventListener("popstate", () => Object.assign(route, current()));

export function navigate(to: string, opts: { replace?: boolean } = {}): void {
  if (opts.replace) history.replaceState(null, "", to);
  else history.pushState(null, "", to);
  Object.assign(route, current());
  window.scrollTo(0, 0);
}

/** Match a pattern against a path; returns params or null. */
export function match(pattern: string, path: string): Record<string, string> | null {
  const p = pattern.split("/").filter(Boolean);
  const s = path.split("/").filter(Boolean);
  if (p.length !== s.length) return null;
  const params: Record<string, string> = {};
  for (let i = 0; i < p.length; i++) {
    const seg = p[i]!;
    if (seg.startsWith(":")) params[seg.slice(1)] = decodeURIComponent(s[i]!);
    else if (seg !== s[i]) return null;
  }
  return params;
}

/** Svelte action: make <a href> links navigate client-side. */
export function link(node: HTMLAnchorElement) {
  const onClick = (e: MouseEvent) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const href = node.getAttribute("href");
    if (!href || !href.startsWith("/") || node.target) return;
    e.preventDefault();
    navigate(href);
  };
  node.addEventListener("click", onClick);
  return { destroy: () => node.removeEventListener("click", onClick) };
}
