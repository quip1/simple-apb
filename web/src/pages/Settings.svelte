<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Settings for this browser: theme and playback. Account and security
  // pages join this screen in the next milestone.
  import { api } from "../api/client";
  import Button from "../components/Button.svelte";
  import { t } from "../lib/i18n";
  import { closeBook } from "../lib/player.svelte";
  import { prefs, savePrefs, type Quality } from "../lib/prefs.svelte";
  import { navigate } from "../lib/router.svelte";
  import { session } from "../lib/session.svelte";
  import { setTheme, themeState, type Theme } from "../lib/theme.svelte";
  import { stopLive } from "../lib/live.svelte";

  const SKIPS = [5, 10, 15, 30, 45, 60];
  const QUALITIES: Quality[] = ["auto", "original", "medium", "low"];
  let signingOut = $state(false);

  async function signOut() {
    signingOut = true;
    await closeBook();
    stopLive();
    await api.POST("/auth/logout");
    session.me = null;
    navigate("/login", { replace: true });
  }
</script>

<svelte:head><title>{t("nav.settings")} · {session.instance?.name ?? ""}</title></svelte:head>

<div class="stack settings">
  <h1>{t("nav.settings")}</h1>

  <section class="card stack" aria-labelledby="s-look">
    <h2 id="s-look">{t("settings.appearance")}</h2>
    <label class="field">
      <span>{t("settings.theme")}</span>
      <select value={themeState.theme} onchange={(e) => setTheme(e.currentTarget.value as Theme)}>
        <option value="dark">{t("settings.theme_dark")}</option>
        <option value="eink">{t("settings.theme_eink")}</option>
      </select>
    </label>
    <label class="field">
      <span>{t("settings.eink_tick")}</span>
      <select
        value={prefs.einkTickS}
        onchange={(e) => {
          prefs.einkTickS = Number(e.currentTarget.value);
          savePrefs();
        }}
      >
        {#each [10, 15, 20, 30] as s (s)}<option value={s}>{t("settings.seconds", { s })}</option>{/each}
      </select>
      <small class="muted">{t("settings.eink_tick_help")}</small>
    </label>
    <label class="check">
      <input
        type="checkbox"
        checked={prefs.leftHanded}
        onchange={(e) => {
          prefs.leftHanded = e.currentTarget.checked;
          savePrefs();
        }}
      />
      <span>{t("settings.left_handed")}</span>
    </label>
  </section>

  <section class="card stack" aria-labelledby="s-play">
    <h2 id="s-play">{t("settings.playback")}</h2>
    <label class="field">
      <span>{t("settings.skip_back")}</span>
      <select
        value={prefs.skipBackS}
        onchange={(e) => {
          prefs.skipBackS = Number(e.currentTarget.value);
          savePrefs();
        }}
      >
        {#each SKIPS as s (s)}<option value={s}>{t("settings.seconds", { s })}</option>{/each}
      </select>
    </label>
    <label class="field">
      <span>{t("settings.skip_forward")}</span>
      <select
        value={prefs.skipForwardS}
        onchange={(e) => {
          prefs.skipForwardS = Number(e.currentTarget.value);
          savePrefs();
        }}
      >
        {#each SKIPS as s (s)}<option value={s}>{t("settings.seconds", { s })}</option>{/each}
      </select>
    </label>
    <label class="field">
      <span>{t("settings.quality")}</span>
      <select
        value={prefs.quality}
        onchange={(e) => {
          prefs.quality = e.currentTarget.value as Quality;
          savePrefs();
        }}
      >
        {#each QUALITIES as q (q)}<option value={q}>{t(`settings.quality_${q}`)}</option>{/each}
      </select>
      <small class="muted">{t("settings.quality_help")}</small>
    </label>
  </section>

  <section class="card stack" aria-labelledby="s-account">
    <h2 id="s-account">{t("settings.account")}</h2>
    <p>{t("settings.signed_in_as", { name: session.me?.display_name ?? "", user: session.me?.username ?? "" })}</p>
    <Button variant="secondary" busy={signingOut} onclick={signOut}>{t("auth.sign_out")}</Button>
  </section>
</div>

<style>
  .settings {
    max-width: 40rem;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .field select {
    max-width: 20rem;
  }
  .check {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    min-height: 48px;
    cursor: pointer;
  }
  .check input {
    width: 1.4rem;
    height: 1.4rem;
    accent-color: var(--accent);
  }
</style>
