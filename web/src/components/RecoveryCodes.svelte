<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Recovery codes, shown once, with a copy button.
  import { t } from "../lib/i18n";
  import Button from "./Button.svelte";

  let { codes }: { codes: string[] } = $props();
  let copied = $state(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(codes.join("\n"));
      copied = true;
    } catch {
      /* clipboard blocked: the codes are on screen to copy by hand */
    }
  }
</script>

<div class="stack">
  <p>{t("setup.done.codes")}</p>
  <ol class="mono codes">
    {#each codes as c (c)}<li>{c}</li>{/each}
  </ol>
  <div class="row">
    <Button variant="secondary" onclick={copy}>{copied ? t("security.codes_copied") : t("security.codes_copy")}</Button>
  </div>
</div>

<style>
  .codes {
    columns: 2;
    column-gap: 2rem;
    padding-left: 1.5rem;
  }
</style>
