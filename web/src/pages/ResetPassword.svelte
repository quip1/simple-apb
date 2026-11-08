<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Set a new password from an admin's reset link. The token is in the URL
  // fragment (#…), so it never reaches server logs or Referer headers.
  import { api } from "../api/client";
  import Alert from "../components/Alert.svelte";
  import AuthShell from "../components/AuthShell.svelte";
  import Button from "../components/Button.svelte";
  import Field from "../components/Field.svelte";
  import { t, tError } from "../lib/i18n";
  import { link } from "../lib/router.svelte";

  const token = location.hash.slice(1);
  let password = $state("");
  let repeat = $state("");
  let error = $state("");
  let busy = $state(false);
  let done = $state(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = "";
    if (password !== repeat) {
      error = t("security.password_mismatch");
      return;
    }
    busy = true;
    const { error: err } = await api.POST("/auth/password-reset", { body: { token, new_password: password } });
    busy = false;
    if (err) {
      error = tError(err.code, err.message);
      return;
    }
    done = true;
    history.replaceState(null, "", location.pathname); // drop the used token from the address bar
  }
</script>

<AuthShell title={t("reset.title")}>
  {#if done}
    <Alert kind="ok">{t("reset.done")}</Alert>
    <p><a href="/login" use:link>{t("reset.sign_in")}</a></p>
  {:else if !token}
    <Alert kind="error">{t("error.invalid_reset_token")}</Alert>
  {:else}
    <form class="stack" onsubmit={submit}>
      <Field
        label={t("security.new_password")}
        type="password"
        bind:value={password}
        autocomplete="new-password"
        hint={t("auth.password_hint")}
        required
      />
      <Field label={t("security.repeat_password")} type="password" bind:value={repeat} autocomplete="new-password" required />
      {#if error}<Alert kind="error">{error}</Alert>{/if}
      <Button type="submit" {busy}>{t("reset.submit")}</Button>
    </form>
  {/if}
</AuthShell>
