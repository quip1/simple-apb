<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Signed-in layout: header navigation, page content, the mini-player bar and
  // a polite live region announcing playback state (brief §11 accessibility).
  import type { Snippet } from "svelte";
  import { t } from "../lib/i18n";
  import { player } from "../lib/player.svelte";
  import { link, route } from "../lib/router.svelte";
  import { session } from "../lib/session.svelte";
  import { themeState } from "../lib/theme.svelte";
  import ConflictPrompt from "./ConflictPrompt.svelte";
  import Icon from "./Icon.svelte";
  import MiniPlayer from "./MiniPlayer.svelte";

  let { children }: { children: Snippet } = $props();

  const tabs = [
    { href: "/", key: "nav.home" },
    { href: "/library", key: "nav.library" },
    { href: "/settings", key: "nav.settings" },
  ];

  function current(href: string): boolean {
    return href === "/" ? route.path === "/" : route.path.startsWith(href);
  }

  // E-ink: flash the screen black then white to clear ghosting (brief §11).
  let flashing = $state(false);
  function refreshScreen() {
    flashing = true;
    setTimeout(() => (flashing = false), 300);
  }

  const showMini = $derived(!!player.book && route.path !== "/player");
</script>

<a class="skip" href="#main">{t("nav.skip_to_content")}</a>
<header class="top">
  <a class="brand" href="/" use:link>{session.instance?.name ?? ""}</a>
  <nav aria-label={t("nav.main")}>
    {#each tabs as tab (tab.href)}
      <a href={tab.href} use:link aria-current={current(tab.href) ? "page" : undefined}>{t(tab.key)}</a>
    {/each}
    {#if player.book}
      <a class="np" href="/player" use:link aria-current={route.path === "/player" ? "page" : undefined}>{t("nav.player")}</a>
    {/if}
  </nav>
  {#if themeState.theme === "eink"}
    <button type="button" class="refresh" onclick={refreshScreen} aria-label={t("nav.refresh_screen")}>
      <Icon name="refresh" />
    </button>
  {/if}
</header>

<main id="main" class="content" class:with-mini={showMini}>
  {@render children()}
</main>

{#if showMini}<MiniPlayer />{/if}
<ConflictPrompt />

<div class="sr-only" aria-live="polite" aria-atomic="true">{player.announce}</div>
{#if flashing}<div class="flash" aria-hidden="true"></div>{/if}

<style>
  .skip {
    position: absolute;
    left: -999px;
    top: 0;
    z-index: 100;
    background: var(--surface);
    padding: 0.75rem 1rem;
  }
  .skip:focus {
    left: 0.5rem;
  }
  .top {
    position: sticky;
    top: 0;
    z-index: 5;
    display: flex;
    align-items: center;
    gap: 1rem;
    padding: 0 max(1rem, env(safe-area-inset-left));
    min-height: 56px;
    background: var(--bg);
    border-bottom: 1px solid var(--border);
  }
  .brand {
    font-weight: 700;
    color: var(--text);
    text-decoration: none;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 40%;
  }
  nav {
    display: flex;
    gap: 0.25rem;
    margin-left: auto;
    overflow-x: auto;
  }
  nav a {
    min-height: 48px;
    display: inline-flex;
    align-items: center;
    padding: 0 0.75rem;
    color: var(--text-muted);
    text-decoration: none;
    border-bottom: 3px solid transparent;
    white-space: nowrap;
  }
  nav a[aria-current="page"] {
    color: var(--text);
    border-bottom-color: var(--accent);
    font-weight: 600;
  }
  .refresh {
    min-width: 48px;
    min-height: 48px;
    background: transparent;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    color: var(--text);
    display: grid;
    place-items: center;
    cursor: pointer;
  }
  .content {
    max-width: 72rem;
    margin: 0 auto;
    padding: 1.25rem max(1rem, env(safe-area-inset-left)) 2rem;
  }
  .with-mini {
    padding-bottom: 7rem;
  }
  .flash {
    position: fixed;
    inset: 0;
    z-index: 1000;
    background: #000;
  }
  @media (max-width: 30rem) {
    .brand {
      display: none;
    }
    /* The mini-player opens the player on phones. */
    .np {
      display: none;
    }
    nav {
      margin-left: 0;
      width: 100%;
      justify-content: space-between;
    }
    nav a {
      padding: 0 0.5rem;
    }
  }
</style>
