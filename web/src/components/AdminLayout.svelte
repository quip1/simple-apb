<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Admin panel frame (brief §6.23): section navigation + page.
  import type { Snippet } from "svelte";
  import { t } from "../lib/i18n";
  import { link, route } from "../lib/router.svelte";
  import { session } from "../lib/session.svelte";

  let { title, children }: { title: string; children: Snippet } = $props();

  const sections = [
    { href: "/admin", key: "admin.nav.dashboard" },
    { href: "/admin/inbox", key: "admin.nav.inbox" },
    { href: "/admin/users", key: "admin.nav.users" },
    { href: "/admin/groups", key: "admin.nav.groups" },
    { href: "/admin/tags", key: "admin.nav.tags" },
    { href: "/admin/library", key: "admin.nav.library" },
    { href: "/admin/duplicates", key: "admin.nav.duplicates" },
    { href: "/admin/trash", key: "admin.nav.trash" },
    { href: "/admin/audit", key: "admin.nav.audit" },
    { href: "/admin/backups", key: "admin.nav.backups" },
    { href: "/admin/updates", key: "admin.nav.updates" },
    { href: "/admin/settings", key: "admin.nav.settings" },
  ];

  function current(href: string): boolean {
    if (href === "/admin") return route.path === "/admin";
    if (href === "/admin/library") return route.path.startsWith("/admin/library") || route.path.startsWith("/admin/books");
    return route.path.startsWith(href);
  }
</script>

<svelte:head><title>{title} · {t("admin.title")} · {session.instance?.name ?? ""}</title></svelte:head>

<div class="admin">
  <nav aria-label={t("admin.title")}>
    <ul>
      {#each sections as s (s.href)}
        <li><a href={s.href} use:link aria-current={current(s.href) ? "page" : undefined}>{t(s.key)}</a></li>
      {/each}
    </ul>
  </nav>
  <div class="page stack">
    <h1>{title}</h1>
    {@render children()}
  </div>
</div>

<style>
  .admin {
    display: grid;
    grid-template-columns: 12rem minmax(0, 1fr);
    gap: 2rem;
    align-items: start;
  }
  nav ul {
    list-style: none;
    margin: 0;
    padding: 0;
    position: sticky;
    top: 5rem;
  }
  nav a {
    display: flex;
    align-items: center;
    min-height: 44px;
    padding: 0 0.75rem;
    border-radius: var(--radius-sm);
    color: var(--text-muted);
    text-decoration: none;
    border-inline-start: 3px solid transparent;
  }
  nav a:hover {
    background: var(--surface-2);
  }
  nav a[aria-current="page"] {
    color: var(--text);
    font-weight: 600;
    border-inline-start-color: var(--accent);
    background: var(--surface);
  }
  @media (max-width: 52rem) {
    .admin {
      grid-template-columns: minmax(0, 1fr);
      gap: 1rem;
    }
    nav {
      min-width: 0;
    }
    nav ul {
      position: static;
      display: flex;
      overflow-x: auto;
      gap: 0.25rem;
      border-bottom: 1px solid var(--border);
    }
    nav a {
      white-space: nowrap;
      border-inline-start: 0;
      border-bottom: 3px solid transparent;
      border-radius: 0;
    }
    nav a[aria-current="page"] {
      border-bottom-color: var(--accent);
      background: transparent;
    }
  }
</style>
