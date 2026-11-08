<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from "svelte";
  import { t } from "./lib/i18n";
  import { match, navigate, route } from "./lib/router.svelte";
  import { loadSession, session } from "./lib/session.svelte";
  import EnrollTotp from "./pages/EnrollTotp.svelte";
  import Home from "./pages/Home.svelte";
  import Login from "./pages/Login.svelte";
  import Setup from "./pages/Setup.svelte";
  import Signup from "./pages/Signup.svelte";

  const PUBLIC = ["/login", "/signup"];

  onMount(loadSession);

  // Routing guard: setup first, then sign-in, then (if required) 2FA enrollment.
  $effect(() => {
    if (!session.loaded) return;
    const inst = session.instance;
    if (inst?.setup_required) {
      if (route.path !== "/setup") navigate("/setup", { replace: true });
      return;
    }
    if (route.path === "/setup") navigate("/", { replace: true });
    else if (!session.me && !PUBLIC.includes(route.path)) {
      const next = route.path === "/" ? "" : `?next=${encodeURIComponent(route.path + location.search)}`;
      navigate(`/login${next}`, { replace: true });
    } else if (session.me && PUBLIC.includes(route.path)) navigate("/", { replace: true });
  });

  // 2FA required but not set up: the server only allows /me and enrollment.
  const mustEnroll = $derived(!!session.me && session.me.role === "admin" && !session.me.totp_enabled);
</script>

{#if !session.loaded}
  <p class="loading" role="status">{t("common.loading")}</p>
{:else if session.instance === null}
  <main class="loading"><p role="alert">{t("error.server_unreachable")}</p></main>
{:else if route.path === "/setup"}
  <Setup />
{:else if match("/login", route.path)}
  <Login />
{:else if match("/signup", route.path)}
  <Signup />
{:else if session.me && mustEnroll}
  <EnrollTotp required />
{:else if session.me}
  {#if match("/", route.path)}
    <Home />
  {:else}
    <main class="loading">
      <h1>{t("error.not_found_title")}</h1>
      <p><a href="/">{t("common.home")}</a></p>
    </main>
  {/if}
{/if}

<style>
  .loading {
    padding: 2rem;
    text-align: center;
  }
</style>
