<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Settings: profile, appearance, playback, sync and privacy; security lives
  // on its own page. Theme and stream quality are per device; the other
  // playback preferences follow the account (prefs.svelte.ts).
  import { api, type Me, type Schemas } from "../api/client";
  import Alert from "../components/Alert.svelte";
  import Button from "../components/Button.svelte";
  import Field from "../components/Field.svelte";
  import { t, tError } from "../lib/i18n";
  import { stopLive } from "../lib/live.svelte";
  import { closeBook } from "../lib/player.svelte";
  import { detachServerPrefs, prefs, savePrefs, type Quality } from "../lib/prefs.svelte";
  import { link, navigate } from "../lib/router.svelte";
  import { session } from "../lib/session.svelte";
  import { setTheme, themeState, type Theme } from "../lib/theme.svelte";

  const SKIPS = [5, 10, 15, 30, 45, 60];
  const QUALITIES: Quality[] = ["auto", "original", "medium", "low"];
  const THRESHOLDS = [1, 2, 3, 5, 10, 15, 30];

  // ── profile ──
  let displayName = $state(session.me?.display_name ?? "");
  let email = $state(session.me?.email ?? "");
  let profileBusy = $state(false);
  let profileError = $state("");
  let profileSaved = $state(false);

  async function patchMe(body: Schemas["MePatch"]): Promise<Me | null> {
    const { data, error } = await api.PATCH("/me", { body });
    if (error || !data) {
      profileError = tError(error?.code, error?.message);
      return null;
    }
    session.me = data;
    return data;
  }

  async function saveProfile(e: SubmitEvent) {
    e.preventDefault();
    profileBusy = true;
    profileError = "";
    profileSaved = false;
    const me = await patchMe({ display_name: displayName.trim(), email: email.trim() });
    profileBusy = false;
    if (me) {
      profileSaved = true;
      displayName = me.display_name;
      email = me.email ?? "";
    }
  }

  function setPref<K extends keyof typeof prefs>(key: K, value: (typeof prefs)[K]) {
    prefs[key] = value;
    savePrefs();
  }

  let signingOut = $state(false);
  async function signOut() {
    signingOut = true;
    await closeBook();
    stopLive();
    detachServerPrefs();
    await api.POST("/auth/logout");
    session.me = null;
    navigate("/login", { replace: true });
  }
</script>

<svelte:head><title>{t("nav.settings")} · {session.instance?.name ?? ""}</title></svelte:head>

<div class="stack settings">
  <h1>{t("nav.settings")}</h1>

  <section class="card stack" aria-labelledby="s-profile">
    <h2 id="s-profile">{t("settings.profile")}</h2>
    <form class="stack" onsubmit={saveProfile}>
      <Field label={t("settings.display_name")} bind:value={displayName} required autocomplete="name" />
      <Field label={t("settings.email")} type="email" bind:value={email} autocomplete="email" hint={t("settings.email_hint")} />
      {#if profileError}<Alert kind="error">{profileError}</Alert>{/if}
      {#if profileSaved}<Alert kind="ok">{t("settings.saved")}</Alert>{/if}
      <div class="row"><Button type="submit" busy={profileBusy}>{t("common.save")}</Button></div>
    </form>
  </section>

  <section class="card stack" aria-labelledby="s-look">
    <h2 id="s-look">{t("settings.appearance")}</h2>
    <label class="field">
      <span>{t("settings.theme")}</span>
      <select value={themeState.theme} onchange={(e) => setTheme(e.currentTarget.value as Theme)}>
        <option value="dark">{t("settings.theme_dark")}</option>
        <option value="eink">{t("settings.theme_eink")}</option>
      </select>
      <small class="muted">{t("settings.this_device")}</small>
    </label>
    <label class="field">
      <span>{t("settings.eink_tick")}</span>
      <select value={prefs.einkTickS} onchange={(e) => setPref("einkTickS", Number(e.currentTarget.value))}>
        {#each [10, 15, 20, 30] as s (s)}<option value={s}>{t("settings.seconds", { s })}</option>{/each}
      </select>
      <small class="muted">{t("settings.eink_tick_help")}</small>
    </label>
    <label class="check">
      <input type="checkbox" checked={prefs.leftHanded} onchange={(e) => setPref("leftHanded", e.currentTarget.checked)} />
      <span>{t("settings.left_handed")}</span>
    </label>
  </section>

  <section class="card stack" aria-labelledby="s-play">
    <h2 id="s-play">{t("settings.playback")}</h2>
    <label class="field">
      <span>{t("settings.skip_back")}</span>
      <select value={prefs.skipBackS} onchange={(e) => setPref("skipBackS", Number(e.currentTarget.value))}>
        {#each SKIPS as s (s)}<option value={s}>{t("settings.seconds", { s })}</option>{/each}
      </select>
    </label>
    <label class="field">
      <span>{t("settings.skip_forward")}</span>
      <select value={prefs.skipForwardS} onchange={(e) => setPref("skipForwardS", Number(e.currentTarget.value))}>
        {#each SKIPS as s (s)}<option value={s}>{t("settings.seconds", { s })}</option>{/each}
      </select>
    </label>
    <label class="field">
      <span>{t("settings.quality")}</span>
      <select value={prefs.quality} onchange={(e) => setPref("quality", e.currentTarget.value as Quality)}>
        {#each QUALITIES as q (q)}<option value={q}>{t(`settings.quality_${q}`)}</option>{/each}
      </select>
      <small class="muted">{t("settings.quality_help")} {t("settings.this_device")}</small>
    </label>
    <label class="field">
      <span>{t("settings.conflict_threshold")}</span>
      <select
        value={session.me?.conflict_threshold_minutes ?? 3}
        onchange={(e) => patchMe({ conflict_threshold_minutes: Number(e.currentTarget.value) })}
      >
        {#each THRESHOLDS as m (m)}<option value={m}>{t("settings.minutes", { m })}</option>{/each}
      </select>
      <small class="muted">{t("settings.conflict_threshold_help")}</small>
    </label>
  </section>

  <section class="card stack" aria-labelledby="s-privacy">
    <h2 id="s-privacy">{t("settings.privacy")}</h2>
    <label class="check">
      <input
        type="checkbox"
        checked={session.me?.listening_visible ?? true}
        onchange={(e) => patchMe({ listening_visible: e.currentTarget.checked })}
      />
      <span>{t("settings.listening_visible")}</span>
    </label>
  </section>

  {#if session.me?.role === "admin"}
    <section class="card stack" aria-labelledby="s-alerts">
      <h2 id="s-alerts">{t("settings.alerts")}</h2>
      <label class="check">
        <input
          type="checkbox"
          checked={session.me?.prefs?.email_alerts === true}
          disabled={!session.me?.email}
          onchange={(e) => patchMe({ prefs: { ...(session.me?.prefs ?? {}), email_alerts: e.currentTarget.checked } })}
        />
        <span>{t("settings.email_alerts")}</span>
      </label>
      <p class="muted">{session.me?.email ? t("settings.email_alerts_hint") : t("settings.email_alerts_no_email")}</p>
    </section>
  {/if}

  <section class="card stack" aria-labelledby="s-account">
    <h2 id="s-account">{t("settings.account")}</h2>
    <p>{t("settings.signed_in_as", { name: session.me?.display_name ?? "", user: session.me?.username ?? "" })}</p>
    <div class="row">
      <a class="btnlink" href="/settings/security" use:link>{t("security.title")}</a>
      <Button variant="secondary" busy={signingOut} onclick={signOut}>{t("auth.sign_out")}</Button>
    </div>
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
    flex: none;
    accent-color: var(--accent);
  }
  .btnlink {
    min-height: 48px;
    display: inline-flex;
    align-items: center;
    padding: 0 1.25rem;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-2);
    color: var(--text);
    font-weight: 600;
    text-decoration: none;
  }
</style>
