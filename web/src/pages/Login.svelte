<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { api, deviceInfo } from "../api/client";
  import Alert from "../components/Alert.svelte";
  import AuthShell from "../components/AuthShell.svelte";
  import Button from "../components/Button.svelte";
  import Field from "../components/Field.svelte";
  import { t, tError } from "../lib/i18n";
  import { link, navigate } from "../lib/router.svelte";
  import { session } from "../lib/session.svelte";

  let username = $state("");
  let password = $state("");
  let challenge = $state("");
  let code = $state("");
  let useRecovery = $state(false);
  let busy = $state(false);
  let error = $state("");

  async function signIn(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = "";
    const r = await api.POST("/auth/login", { body: { username, password, device: deviceInfo() } });
    busy = false;
    if (r.error) {
      error = tError(r.error.code, r.error.message);
      return;
    }
    if (r.data.status === "totp_required") {
      challenge = r.data.totp_challenge ?? "";
      queueMicrotask(() => document.getElementById("code")?.focus());
      return;
    }
    done(r.data.user);
  }

  async function verify(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = "";
    const body = useRecovery ? { totp_challenge: challenge, recovery_code: code.trim() } : { totp_challenge: challenge, code: code.trim() };
    const r = await api.POST("/auth/totp/verify", { body });
    busy = false;
    if (r.error) {
      error = tError(r.error.code, r.error.message);
      if (r.error.code === "invalid_token") challenge = ""; // challenge expired: start over
      return;
    }
    done(r.data.user);
  }

  function done(user: typeof session.me | undefined) {
    session.me = user ?? null;
    session.notice = user?.notice ?? "";
    const next = new URLSearchParams(location.search).get("next");
    navigate(next && next.startsWith("/") && !next.startsWith("//") ? next : "/", { replace: true });
  }
</script>

<AuthShell title={t("auth.sign_in")}>
  {#if !challenge}
    <form class="stack" onsubmit={signIn}>
      <Field label={t("auth.username")} bind:value={username} autocomplete="username" required />
      <Field label={t("auth.password")} type="password" bind:value={password} autocomplete="current-password" required />
      {#if error}<Alert kind="error">{error}</Alert>{/if}
      <Button type="submit" {busy}>{t("auth.sign_in")}</Button>
    </form>
    {#if session.instance?.email_password_reset}
      <p><a href="/forgot-password" use:link>{t("forgot.link")}</a></p>
    {/if}
    {#if session.instance?.signup_open}
      <p>{t("auth.no_account")} <a href="/signup" use:link>{t("auth.request_account")}</a></p>
    {/if}
  {:else}
    <form class="stack" onsubmit={verify}>
      <p>{useRecovery ? t("auth.recovery_help") : t("auth.totp_help")}</p>
      <Field
        id="code"
        label={useRecovery ? t("auth.recovery_code") : t("auth.totp_code")}
        bind:value={code}
        inputmode={useRecovery ? "text" : "numeric"}
        autocomplete="one-time-code"
        required
      />
      {#if error}<Alert kind="error">{error}</Alert>{/if}
      <Button type="submit" {busy}>{t("auth.verify")}</Button>
      <Button
        variant="ghost"
        onclick={() => {
          useRecovery = !useRecovery;
          code = "";
        }}
      >
        {useRecovery ? t("auth.use_app_code") : t("auth.use_recovery")}
      </Button>
    </form>
  {/if}
</AuthShell>
