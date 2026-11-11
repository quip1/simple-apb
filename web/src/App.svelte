<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from "svelte";
  import AppShell from "./components/AppShell.svelte";
  import { t } from "./lib/i18n";
  import { live, startLive, stopLive } from "./lib/live.svelte";
  import { followRemoteProgress, restoreLastBook } from "./lib/player.svelte";
  import { applyServerPrefs, detachServerPrefs } from "./lib/prefs.svelte";
  import { match, navigate, route } from "./lib/router.svelte";
  import { loadSession, refreshMe, session } from "./lib/session.svelte";
  import BookDetail from "./pages/BookDetail.svelte";
  import EnrollTotp from "./pages/EnrollTotp.svelte";
  import Home from "./pages/Home.svelte";
  import Library from "./pages/Library.svelte";
  import Login from "./pages/Login.svelte";
  import PersonPage from "./pages/PersonPage.svelte";
  import ForgotPassword from "./pages/ForgotPassword.svelte";
  import ResetPassword from "./pages/ResetPassword.svelte";
  import Player from "./pages/Player.svelte";
  import Security from "./pages/Security.svelte";
  import Settings from "./pages/Settings.svelte";
  import Stats from "./pages/Stats.svelte";
  import Uploads from "./pages/Uploads.svelte";
  import Setup from "./pages/Setup.svelte";
  import Signup from "./pages/Signup.svelte";
  import Connect from "./pages/desktop/Connect.svelte";
  import Downloads from "./pages/desktop/Downloads.svelte";
  import Button from "./components/Button.svelte";
  import { DESKTOP, checkUpdate, desktop, loadInfo, refreshDownloads } from "./lib/desktop.svelte";
  import { filesFromDrop, pending } from "./lib/dropbook";

  const PUBLIC = ["/login", "/signup"];
  // Reachable signed in or out.
  const OPEN = ["/reset-password", "/forgot-password"];
  const loadAdmin = () => import("./pages/admin/AdminRoutes.svelte");

  // The Windows app first needs a server (ADR-0047).
  async function start() {
    if (DESKTOP) {
      const info = await loadInfo();
      if (!info?.server_url) {
        desktop.choosingServer = true;
        return;
      }
      void refreshDownloads();
      void checkUpdate();
    }
    await loadSession();
  }
  onMount(start);

  function connected() {
    desktop.choosingServer = false;
    session.loaded = false;
    session.me = null;
    navigate("/", { replace: true });
    void start();
  }

  // Windows app: files or folders dropped anywhere on the window are uploaded.
  function onWindowDrop(e: DragEvent) {
    if (!DESKTOP || !e.dataTransfer || !session.me) return;
    e.preventDefault();
    void filesFromDrop(e.dataTransfer).then((files) => {
      if (!files.length) return;
      pending.push(...files);
      if (route.path !== "/uploads") navigate("/uploads");
    });
  }
  function onWindowDragOver(e: DragEvent) {
    if (DESKTOP && session.me) e.preventDefault();
  }

  // Routing guard: setup first, then sign-in, then (if required) 2FA enrollment.
  $effect(() => {
    if (!session.loaded) return;
    const inst = session.instance;
    if (inst?.setup_required) {
      if (route.path !== "/setup") navigate("/setup", { replace: true });
      return;
    }
    if (OPEN.includes(route.path)) return;
    if (route.path === "/setup") navigate("/", { replace: true });
    else if (!session.me && !PUBLIC.includes(route.path)) {
      const next = route.path === "/" ? "" : `?next=${encodeURIComponent(route.path + location.search)}`;
      navigate(`/login${next}`, { replace: true });
    } else if (session.me && PUBLIC.includes(route.path)) navigate("/", { replace: true });
  });

  // 2FA required but not set up: the server only allows /me and enrollment.
  const mustEnroll = $derived(!!session.me && session.me.totp_required && !session.me.totp_enabled);
  const ready = $derived(!!session.me && !mustEnroll);

  // Signed in: live sync hints, and bring back the last book (paused).
  $effect(() => {
    if (!ready) return;
    startLive();
    void restoreLastBook();
    return () => {
      stopLive();
      detachServerPrefs();
    };
  });
  // Synced preferences follow the account (and other devices' changes).
  $effect(() => {
    if (ready && session.me) applyServerPrefs(session.me.prefs);
  });
  $effect(() => {
    if (ready && live.tick > 0) void refreshMe();
  });
  $effect(() => {
    if (live.tick > 0) void followRemoteProgress();
  });

  const params = $derived.by(() => {
    for (const p of ["/books/:id", "/authors/:id", "/narrators/:id", "/series/:id"]) {
      const m = match(p, route.path);
      if (m) return { pattern: p, id: m.id! };
    }
    return null;
  });
</script>

<svelte:window ondrop={onWindowDrop} ondragover={onWindowDragOver} />

{#if DESKTOP && desktop.choosingServer}
  <Connect onConnected={connected} current={desktop.info?.server_url ?? ""} />
{:else if !session.loaded}
  <p class="loading" role="status">{t("common.loading")}</p>
{:else if session.instance === null}
  <main class="loading stack">
    <p role="alert">{t("error.server_unreachable")}</p>
    {#if DESKTOP}
      <div><Button variant="secondary" onclick={() => (desktop.choosingServer = true)}>{t("desktop.server.change")}</Button></div>
    {/if}
  </main>
{:else if route.path === "/setup"}
  <Setup />
{:else if match("/reset-password", route.path)}
  <ResetPassword />
{:else if match("/forgot-password", route.path)}
  <ForgotPassword />
{:else if match("/login", route.path)}
  <Login />
{:else if match("/signup", route.path)}
  <Signup />
{:else if session.me && mustEnroll}
  <EnrollTotp required />
{:else if session.me}
  <AppShell>
    {#if match("/", route.path)}
      <Home />
    {:else if match("/library", route.path)}
      <Library />
    {:else if match("/player", route.path)}
      <Player />
    {:else if match("/settings", route.path)}
      <Settings />
    {:else if match("/settings/security", route.path)}
      <Security />
    {:else if match("/stats", route.path)}
      <Stats />
    {:else if match("/uploads", route.path)}
      <Uploads />
    {:else if DESKTOP && match("/downloads", route.path)}
      <Downloads />
    {:else if route.path === "/admin" || route.path.startsWith("/admin/")}
      {#if session.me.role === "admin"}
        {#await loadAdmin()}
          <p class="muted" role="status">{t("common.loading")}</p>
        {:then m}
          <m.default />
        {/await}
      {:else}
        <div class="loading"><h1>{t("error.not_found_title")}</h1></div>
      {/if}
    {:else if params?.pattern === "/books/:id"}
      {#key params.id}<BookDetail id={params.id} />{/key}
    {:else if params?.pattern === "/authors/:id"}
      <PersonPage kind="author" id={params.id} />
    {:else if params?.pattern === "/narrators/:id"}
      <PersonPage kind="narrator" id={params.id} />
    {:else if params?.pattern === "/series/:id"}
      <PersonPage kind="series" id={params.id} />
    {:else}
      <div class="loading">
        <h1>{t("error.not_found_title")}</h1>
        <p><a href="/">{t("common.home")}</a></p>
      </div>
    {/if}
  </AppShell>
{/if}

<style>
  .loading {
    padding: 2rem;
    text-align: center;
  }
</style>
