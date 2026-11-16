<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // The Windows app's "Update now" (ADR-0052): download with a progress bar,
  // then the installer runs and the app reopens on the new version. If that
  // fails, the release page is offered instead.
  import { installUpdate, openExternal, updateProgress, type UpdateInfo } from "../lib/desktop.svelte";
  import { t } from "../lib/i18n";
  import Button from "./Button.svelte";

  let { update }: { update: UpdateInfo } = $props();
</script>

{#if updateProgress.phase === "downloading"}
  <span role="status">{t("desktop.update.downloading", { percent: updateProgress.percent })}</span>
  <progress max="100" value={updateProgress.percent} aria-label={t("desktop.update.downloading", { percent: updateProgress.percent })}
  ></progress>
{:else if updateProgress.phase === "installing"}
  <span role="status">{t("desktop.update.installing")}</span>
{:else}
  {#if updateProgress.phase === "failed"}
    <span class="error" role="alert">{t("desktop.update.failed")}</span>
  {/if}
  <Button onclick={() => void installUpdate()}>{t("desktop.update.install")}</Button>
  {#if updateProgress.phase === "failed" && update.url}
    {@const url = update.url}
    <Button variant="ghost" onclick={() => openExternal(url)}>{t("desktop.update.page")}</Button>
  {/if}
{/if}

<style>
  .error {
    color: var(--danger);
  }
</style>
