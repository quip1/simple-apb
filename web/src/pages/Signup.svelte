<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { api } from "../api/client";
  import Alert from "../components/Alert.svelte";
  import AuthShell from "../components/AuthShell.svelte";
  import Button from "../components/Button.svelte";
  import Field from "../components/Field.svelte";
  import { t, tError } from "../lib/i18n";
  import { link } from "../lib/router.svelte";
  import { session } from "../lib/session.svelte";

  let username = $state("");
  let password = $state("");
  let password2 = $state("");
  let email = $state("");
  let message = $state("");
  let age = $state(false);
  let terms = $state(false);
  let busy = $state(false);
  let error = $state("");
  let sent = $state(false);
  let showTerms = $state(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = "";
    if (password !== password2) return (error = t("setup.admin.mismatch"));
    busy = true;
    const r = await api.POST("/auth/signup", {
      body: { username, password, email: email || undefined, message: message || undefined, age_confirmed: age, terms_accepted: terms },
    });
    busy = false;
    if (r.error) return (error = tError(r.error.code, r.error.message));
    sent = true;
  }
</script>

<AuthShell title={t("auth.request_account")}>
  {#if sent}
    <Alert kind="ok">{t("auth.signup_sent")}</Alert>
    <p><a href="/login" use:link>{t("auth.back_to_sign_in")}</a></p>
  {:else if !session.instance?.signup_open}
    <Alert kind="info">{t("error.signup_closed")}</Alert>
  {:else}
    <form class="stack" onsubmit={submit}>
      <Field label={t("auth.username")} bind:value={username} autocomplete="username" required hint={t("auth.username_hint")} />
      <Field
        label={t("auth.password")}
        type="password"
        bind:value={password}
        autocomplete="new-password"
        required
        hint={t("auth.password_hint")}
      />
      <Field label={t("auth.password_repeat")} type="password" bind:value={password2} autocomplete="new-password" required />
      <Field label={t("auth.email_optional")} type="email" bind:value={email} autocomplete="email" hint={t("auth.email_hint")} />
      <Field label={t("auth.message")} bind:value={message} multiline rows={3} hint={t("auth.message_hint")} />
      <label class="choice"><input type="checkbox" bind:checked={age} required /> <span>{t("auth.age_confirm")}</span></label>
      <label class="choice"
        ><input type="checkbox" bind:checked={terms} required />
        <span
          >{t("auth.terms_accept")}
          <button type="button" class="linklike" onclick={() => (showTerms = !showTerms)} aria-expanded={showTerms}
            >{t("auth.read_terms")}</button
          ></span
        >
      </label>
      {#if showTerms}
        <div class="legal card">
          <h2>{t("setup.instance.terms")}</h2>
          <p class="pre">{session.instance?.terms_text || t("auth.no_terms")}</p>
          <h2>{t("setup.instance.privacy")}</h2>
          <p class="pre">{session.instance?.privacy_text || t("auth.no_terms")}</p>
        </div>
      {/if}
      {#if error}<Alert kind="error">{error}</Alert>{/if}
      <Button type="submit" {busy}>{t("auth.send_request")}</Button>
    </form>
    <p><a href="/login" use:link>{t("auth.back_to_sign_in")}</a></p>
  {/if}
</AuthShell>

<style>
  .choice {
    display: flex;
    gap: 0.75rem;
    align-items: flex-start;
    min-height: 48px;
  }
  .choice input {
    width: 1.25rem;
    height: 1.25rem;
    margin-top: 0.2rem;
  }
  .linklike {
    background: none;
    border: 0;
    padding: 0;
    color: var(--accent);
    text-decoration: underline;
    font: inherit;
    cursor: pointer;
  }
  .pre {
    white-space: pre-wrap;
    max-height: 16rem;
    overflow: auto;
  }
  .legal {
    padding: 1rem;
  }
</style>
