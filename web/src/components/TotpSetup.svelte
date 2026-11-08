<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Two-factor enrollment: QR code (rendered in the browser) → confirm a code
  // → show the recovery codes once.
  import QRCode from "qrcode";
  import { onMount } from "svelte";
  import { api } from "../api/client";
  import { t, tError } from "../lib/i18n";
  import { refreshMe } from "../lib/session.svelte";
  import Alert from "./Alert.svelte";
  import Button from "./Button.svelte";
  import Field from "./Field.svelte";
  import RecoveryCodes from "./RecoveryCodes.svelte";

  let { required = false, ondone }: { required?: boolean; ondone?: () => void } = $props();
  let secret = $state("");
  let qr = $state("");
  let code = $state("");
  let codes = $state<string[]>([]);
  let error = $state("");
  let busy = $state(false);

  onMount(async () => {
    const r = await api.POST("/auth/totp/enroll");
    if (r.error) return (error = tError(r.error.code, r.error.message));
    secret = r.data.secret;
    qr = await QRCode.toDataURL(r.data.otpauth_uri, { margin: 1, width: 220 });
  });

  async function confirm(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    const r = await api.POST("/auth/totp/confirm", { body: { code: code.trim() } });
    busy = false;
    if (r.error) return (error = tError(r.error.code, r.error.message));
    codes = r.data.codes;
  }
</script>

<div class="stack">
  {#if codes.length}
    <RecoveryCodes {codes} />
    <Button
      onclick={async () => {
        await refreshMe();
        ondone?.();
      }}>{t("common.continue")}</Button
    >
  {:else}
    {#if required}<Alert kind="info">{t("totp.required")}</Alert>{/if}
    <p>{t("setup.admin.totp_help")}</p>
    {#if qr}<img src={qr} alt={t("setup.admin.qr_alt")} width="220" height="220" class="qr" />{/if}
    {#if secret}<p class="muted">{t("setup.admin.secret")} <code>{secret}</code></p>{/if}
    <form class="stack" onsubmit={confirm}>
      <Field label={t("auth.totp_code")} bind:value={code} inputmode="numeric" autocomplete="one-time-code" required />
      {#if error}<Alert kind="error">{error}</Alert>{/if}
      <Button type="submit" {busy}>{t("totp.enable")}</Button>
    </form>
  {/if}
</div>

<style>
  .qr {
    background: #fff;
    padding: 8px;
  }
</style>
