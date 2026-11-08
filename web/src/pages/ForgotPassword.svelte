<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // "Forgot your password?": asks for a reset email (brief §6.6). The answer
  // is the same whether or not the account exists.
  import { api } from "../api/client";
  import Alert from "../components/Alert.svelte";
  import AuthShell from "../components/AuthShell.svelte";
  import Button from "../components/Button.svelte";
  import Field from "../components/Field.svelte";
  import { t, tError } from "../lib/i18n";
  import { link } from "../lib/router.svelte";
  import { session } from "../lib/session.svelte";

  let login = $state("");
  let error = $state("");
  let busy = $state(false);
  let sent = $state(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = "";
    busy = true;
    const { error: err } = await api.POST("/auth/password-reset/request", { body: { login: login.trim() } });
    busy = false;
    if (err) error = tError(err.code, err.message);
    else sent = true;
  }
</script>

<AuthShell title={t("forgot.title")}>
  {#if sent}
    <Alert kind="ok">{t("forgot.sent")}</Alert>
    <p><a href="/login" use:link>{t("reset.sign_in")}</a></p>
  {:else if !session.instance?.email_password_reset}
    <p>{t("forgot.no_email")}</p>
    <p><a href="/login" use:link>{t("reset.sign_in")}</a></p>
  {:else}
    <form class="stack" onsubmit={submit}>
      <p>{t("forgot.intro")}</p>
      <Field label={t("forgot.login")} bind:value={login} autocomplete="username" required />
      {#if error}<Alert kind="error">{error}</Alert>{/if}
      <Button type="submit" {busy}>{t("forgot.submit")}</Button>
    </form>
    <p><a href="/login" use:link>{t("reset.sign_in")}</a></p>
  {/if}
</AuthShell>
