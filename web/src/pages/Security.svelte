<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Security: password, two-factor login, recovery codes and signed-in devices.
  import { api, type Schemas } from "../api/client";
  import Alert from "../components/Alert.svelte";
  import Button from "../components/Button.svelte";
  import Field from "../components/Field.svelte";
  import RecoveryCodes from "../components/RecoveryCodes.svelte";
  import TotpSetup from "../components/TotpSetup.svelte";
  import { ago } from "../lib/format";
  import { t, tError } from "../lib/i18n";
  import { live } from "../lib/live.svelte";
  import { link } from "../lib/router.svelte";
  import { refreshMe, session } from "../lib/session.svelte";

  // ── password ──
  let current = $state("");
  let next = $state("");
  let repeat = $state("");
  let pwBusy = $state(false);
  let pwError = $state("");
  let pwDone = $state(false);

  async function changePassword(e: SubmitEvent) {
    e.preventDefault();
    pwError = "";
    pwDone = false;
    if (next !== repeat) {
      pwError = t("security.password_mismatch");
      return;
    }
    pwBusy = true;
    const { error } = await api.POST("/me/password", { body: { current_password: current, new_password: next } });
    pwBusy = false;
    if (error) {
      pwError = tError(error.code, error.message);
      return;
    }
    current = next = repeat = "";
    pwDone = true;
    await loadSessions();
  }

  // ── two-factor ──
  let enrolling = $state(false);
  let codeForCodes = $state("");
  let newCodes = $state<string[]>([]);
  let codesBusy = $state(false);
  let codesError = $state("");

  async function regenerate(e: SubmitEvent) {
    e.preventDefault();
    codesBusy = true;
    codesError = "";
    const { data, error } = await api.POST("/me/recovery-codes", { body: { code: codeForCodes.trim() } });
    codesBusy = false;
    if (error || !data) {
      codesError = tError(error?.code, error?.message);
      return;
    }
    codeForCodes = "";
    newCodes = data.codes;
  }

  let offPassword = $state("");
  let offCode = $state("");
  let offBusy = $state(false);
  let offError = $state("");

  async function disable(e: SubmitEvent) {
    e.preventDefault();
    offBusy = true;
    offError = "";
    const { error } = await api.POST("/me/totp/disable", { body: { password: offPassword, code: offCode.trim() } });
    offBusy = false;
    if (error) {
      offError = tError(error.code, error.message);
      return;
    }
    offPassword = offCode = "";
    newCodes = [];
    await refreshMe();
  }

  // ── devices ──
  let sessions = $state<Schemas["Session"][]>([]);
  let sessError = $state("");

  async function loadSessions() {
    const { data, error } = await api.GET("/me/sessions");
    if (error) sessError = tError(error.code, error.message);
    else sessions = data ?? [];
  }
  $effect(() => {
    void live.tick;
    void loadSessions();
  });

  async function revoke(id: string) {
    await api.DELETE("/me/sessions/{sessionId}", { params: { path: { sessionId: id } } });
    await loadSessions();
  }
  async function revokeOthers() {
    await api.DELETE("/me/sessions");
    await loadSessions();
  }

  const others = $derived(sessions.filter((s) => !s.current).length);
</script>

<svelte:head><title>{t("security.title")} · {session.instance?.name ?? ""}</title></svelte:head>

<div class="stack page">
  <p><a href="/settings" use:link>← {t("nav.settings")}</a></p>
  <h1>{t("security.title")}</h1>

  <section class="card stack" aria-labelledby="sec-pw">
    <h2 id="sec-pw">{t("security.password")}</h2>
    <form class="stack" onsubmit={changePassword}>
      <Field label={t("security.current_password")} type="password" bind:value={current} autocomplete="current-password" required />
      <Field
        label={t("security.new_password")}
        type="password"
        bind:value={next}
        autocomplete="new-password"
        hint={t("auth.password_hint")}
        required
      />
      <Field label={t("security.repeat_password")} type="password" bind:value={repeat} autocomplete="new-password" required />
      {#if pwError}<Alert kind="error">{pwError}</Alert>{/if}
      {#if pwDone}<Alert kind="ok">{t("security.password_changed")}</Alert>{/if}
      <div class="row"><Button type="submit" busy={pwBusy}>{t("security.change_password")}</Button></div>
    </form>
  </section>

  <section class="card stack" aria-labelledby="sec-2fa">
    <h2 id="sec-2fa">{t("security.totp")}</h2>
    {#if session.me?.totp_enabled}
      <p>{t("security.totp_on")}</p>

      <h3>{t("security.recovery_codes")}</h3>
      {#if newCodes.length}
        <RecoveryCodes codes={newCodes} />
      {:else}
        <p class="muted">{t("security.recovery_help")}</p>
        <form class="stack" onsubmit={regenerate}>
          <Field label={t("auth.totp_code")} bind:value={codeForCodes} inputmode="numeric" autocomplete="one-time-code" required />
          {#if codesError}<Alert kind="error">{codesError}</Alert>{/if}
          <div class="row"><Button variant="secondary" type="submit" busy={codesBusy}>{t("security.new_codes")}</Button></div>
        </form>
      {/if}

      <h3>{t("security.totp_off_title")}</h3>
      {#if session.me.totp_required}
        <p class="muted">{t("security.totp_required")}</p>
      {:else}
        <form class="stack" onsubmit={disable}>
          <Field label={t("security.current_password")} type="password" bind:value={offPassword} autocomplete="current-password" required />
          <Field label={t("auth.totp_code")} bind:value={offCode} inputmode="numeric" autocomplete="one-time-code" required />
          {#if offError}<Alert kind="error">{offError}</Alert>{/if}
          <div class="row"><Button variant="danger" type="submit" busy={offBusy}>{t("security.totp_disable")}</Button></div>
        </form>
      {/if}
    {:else if enrolling}
      <TotpSetup ondone={() => (enrolling = false)} />
    {:else}
      <p>{t("security.totp_off")}</p>
      <div class="row"><Button onclick={() => (enrolling = true)}>{t("security.totp_enable")}</Button></div>
    {/if}
  </section>

  <section class="card stack" aria-labelledby="sec-devices">
    <h2 id="sec-devices">{t("security.devices")}</h2>
    {#if sessError}<Alert kind="error">{sessError}</Alert>{/if}
    <ul class="devices">
      {#each sessions as s (s.id)}
        <li>
          <div class="dev">
            <strong>{s.device_name}</strong>
            {#if s.current}<span class="badge">{t("security.this_device")}</span>{/if}
            <span class="muted">{t("security.device_meta", { platform: s.platform, seen: ago(s.last_seen_at) })}</span>
          </div>
          {#if !s.current}
            <Button variant="ghost" onclick={() => revoke(s.id)}>{t("security.sign_out_device")}</Button>
          {/if}
        </li>
      {/each}
    </ul>
    {#if others > 0}
      <div class="row"><Button variant="secondary" onclick={revokeOthers}>{t("security.sign_out_others")}</Button></div>
    {/if}
  </section>
</div>

<style>
  .page {
    max-width: 40rem;
  }
  h3 {
    margin: 1rem 0 0;
    font-size: 1.05rem;
  }
  .devices {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .devices li {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 1rem;
    padding: 0.6rem 0;
  }
  .devices li + li {
    border-top: 1px solid var(--border);
  }
  .dev {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }
  .badge {
    align-self: flex-start;
    font-size: 0.75rem;
    font-weight: 600;
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 0 0.5rem;
  }
</style>
