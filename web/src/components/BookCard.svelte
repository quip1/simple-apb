<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import type { BookSummary } from "../api/client";
  import { duration, percent, seriesLabel } from "../lib/format";
  import { t } from "../lib/i18n";
  import { link } from "../lib/router.svelte";
  import Cover from "./Cover.svelte";
  import ProgressBar from "./ProgressBar.svelte";

  let { book, layout = "grid" }: { book: BookSummary; layout?: "grid" | "list" } = $props();

  const finished = $derived(!!book.progress?.finished_at);
  const pct = $derived(percent(book.progress?.position_ms ?? 0, book.duration_ms));
  const authors = $derived(book.authors.map((a) => a.name).join(", "));
</script>

<a class="book {layout}" href="/books/{book.id}" use:link>
  <div class="art"><Cover src={book.cover_url} has={book.has_cover} title={book.title} size={layout === "grid" ? 256 : 128} /></div>
  <div class="text">
    <span class="title">{book.title}</span>
    {#if authors}<span class="muted sub">{authors}</span>{/if}
    {#if layout === "list"}
      <span class="muted sub">
        {#if book.series}
          {seriesLabel(book.series)} ·
        {/if}
        {duration(book.duration_ms)}
      </span>
    {/if}
    {#if finished}
      <span class="badge">{t("book.finished")}</span>
    {:else if pct > 0}
      <span class="progress">
        <ProgressBar value={pct} label={t("book.progress_label", { pct })} />
        <span class="muted pct" aria-hidden="true">{t("book.percent", { pct })}</span>
      </span>
    {/if}
  </div>
</a>

<style>
  .book {
    display: flex;
    color: inherit;
    text-decoration: none;
    border-radius: var(--radius);
    min-width: 0;
  }
  .book:hover .title {
    text-decoration: underline;
  }
  .grid {
    flex-direction: column;
    gap: 0.5rem;
  }
  .list {
    gap: 0.9rem;
    align-items: center;
    padding: 0.4rem 0;
  }
  .list .art {
    width: 4.5rem;
    flex: none;
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    min-width: 0;
    flex: 1;
  }
  .title {
    font-weight: 600;
    overflow: hidden;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
  }
  .sub {
    font-size: 0.875rem;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .progress {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-top: 0.2rem;
  }
  .progress :global(.bar) {
    flex: 1;
  }
  .pct {
    font-size: 0.8rem;
  }
  .badge {
    align-self: flex-start;
    font-size: 0.75rem;
    font-weight: 600;
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 0 0.5rem;
    margin-top: 0.2rem;
  }
</style>
