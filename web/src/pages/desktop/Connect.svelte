<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // The Windows app's first screen: which server to use (ADR-0047). There is
  // no default address; the app checks the address answers as Simple APB.
  import Alert from "../../components/Alert.svelte";
  import AuthShell from "../../components/AuthShell.svelte";
  import Button from "../../components/Button.svelte";
  import Field from "../../components/Field.svelte";
  import { setServer, type ServerError } from "../../lib/desktop.svelte";
  import { t } from "../../lib/i18n";

  let { onConnected, current = "" }: { onConnected: () => void; current?: string } = $props();

  // svelte-ignore state_referenced_locally
  let url = $state(current);
  let busy = $state(false);
  let failed = $state<ServerError | null>(null);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    failed = await setServer(url);
    busy = false;
    if (!failed) onConnected();
  }
</script>

<svelte:head><title>{t("desktop.connect.title")}</title></svelte:head>

<AuthShell title={t("desktop.connect.title")}>
  <form class="stack" onsubmit={submit}>
    <p>{t("desktop.connect.body")}</p>
    {#if failed}
      <Alert kind="error">{t(`desktop.connect.error_${failed}`)}</Alert>
    {/if}
    <Field
      label={t("desktop.connect.address")}
      hint={t("desktop.connect.address_hint")}
      bind:value={url}
      type="url"
      inputmode="url"
      required
    />
    <Button type="submit" {busy}>{t("common.continue")}</Button>
  </form>
</AuthShell>
