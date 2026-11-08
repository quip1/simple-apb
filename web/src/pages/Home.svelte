<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Placeholder home until the player screens land (Phase 2, milestone 2). -->
<script lang="ts">
  import { api } from "../api/client";
  import Button from "../components/Button.svelte";
  import { t } from "../lib/i18n";
  import { navigate } from "../lib/router.svelte";
  import { session } from "../lib/session.svelte";
  import { getTheme, setTheme, type Theme } from "../lib/theme";

  let theme = $state<Theme>(getTheme());

  async function signOut() {
    await api.POST("/auth/logout");
    session.me = null;
    navigate("/login", { replace: true });
  }
</script>

<main class="page stack">
  <header class="row top">
    <strong>{session.instance?.name}</strong>
    <div class="row">
      <label class="row"
        >{t("settings.theme")}
        <select bind:value={theme} onchange={() => setTheme(theme)}>
          <option value="dark">{t("settings.theme_dark")}</option>
          <option value="eink">{t("settings.theme_eink")}</option>
        </select>
      </label>
      <Button variant="secondary" onclick={signOut}>{t("auth.sign_out")}</Button>
    </div>
  </header>
  <h1>{t("home.welcome", { name: session.me?.display_name ?? "" })}</h1>
  <p class="muted">{t("home.coming")}</p>
</main>

<style>
  .page {
    max-width: 64rem;
    margin: 0 auto;
    padding: 1rem;
  }
  .top {
    justify-content: space-between;
  }
  select {
    min-height: 48px;
    background: var(--surface-2);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 0 0.6rem;
    font: inherit;
  }
</style>
