<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Downloaded books on this computer (brief §10): they play with no network.
  import Button from "../../components/Button.svelte";
  import ProgressBar from "../../components/ProgressBar.svelte";
  import { desktop, pickDownloadFolder, refreshDownloads, removeDownload, download } from "../../lib/desktop.svelte";
  import { bytes } from "../../lib/format";
  import { t } from "../../lib/i18n";
  import { link } from "../../lib/router.svelte";
  import { session } from "../../lib/session.svelte";

  const jobs = $derived(Object.values(desktop.downloads).sort((a, b) => a.title.localeCompare(b.title)));
  const used = $derived(jobs.filter((j) => j.status === "done").reduce((n, j) => n + j.total_bytes, 0));

  $effect(() => {
    void refreshDownloads();
  });

  function pct(done: number, total: number): number {
    return total > 0 ? Math.round((done / total) * 100) : 0;
  }
</script>

<svelte:head><title>{t("desktop.downloads.title")} · {session.instance?.name ?? ""}</title></svelte:head>

<div class="stack">
  <h1>{t("desktop.downloads.title")}</h1>
  <p class="muted">{t("desktop.downloads.body")}</p>
  <section class="card stack" aria-labelledby="d-folder">
    <h2 id="d-folder">{t("desktop.downloads.folder")}</h2>
    <p class="path">{desktop.info?.download_dir ?? ""}</p>
    <p class="muted">{t("desktop.downloads.used", { size: bytes(used) })}</p>
    <div><Button variant="secondary" onclick={() => void pickDownloadFolder()}>{t("desktop.downloads.change_folder")}</Button></div>
  </section>
  {#if !jobs.length}
    <p>{t("desktop.downloads.none")}</p>
  {:else}
    <ul class="list stack">
      {#each jobs as j (j.book_id)}
        <li class="card row">
          <div class="grow stack-tight">
            <a href="/books/{j.book_id}" use:link class="title">{j.title || t("common.loading")}</a>
            {#if j.status === "done"}
              <span class="muted">{t("desktop.downloads.done", { size: bytes(j.total_bytes) })}</span>
            {:else if j.status === "failed"}
              <span class="error">{t(j.error === "checksum" ? "desktop.downloads.failed_checksum" : "desktop.downloads.failed")}</span>
            {:else if j.status === "queued"}
              <span class="muted">{t("desktop.downloads.queued")}</span>
            {:else}
              <ProgressBar value={pct(j.done_bytes, j.total_bytes)} label={t("desktop.downloads.progress", { title: j.title })} />
              <span class="muted">{t("desktop.downloads.downloading", { done: bytes(j.done_bytes), total: bytes(j.total_bytes) })}</span>
            {/if}
          </div>
          {#if j.status === "failed"}
            <Button variant="secondary" onclick={() => void download(j.book_id)}>{t("desktop.downloads.retry")}</Button>
          {/if}
          <Button variant="ghost" onclick={() => void removeDownload(j.book_id)}>
            {j.status === "done" ? t("desktop.downloads.remove") : t("common.cancel")}
          </Button>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .list {
    list-style: none;
    padding: 0;
    margin: 0;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 1rem;
    flex-wrap: wrap;
  }
  .grow {
    flex: 1 1 16rem;
    min-width: 0;
  }
  .stack-tight {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }
  .title {
    font-weight: 600;
  }
  .path {
    font-family: ui-monospace, monospace;
    overflow-wrap: anywhere;
  }
  .error {
    color: var(--danger);
  }
</style>
