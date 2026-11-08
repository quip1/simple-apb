<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Chapter list; paged in e-ink (brief §11), scrolls otherwise.
  import type { Book } from "../api/client";
  import { clock, duration } from "../lib/format";
  import { t } from "../lib/i18n";
  import { themeState } from "../lib/theme.svelte";
  import Pager from "./Pager.svelte";

  type Chapter = Book["chapters"][number];
  let { chapters, current = -1, onselect }: { chapters: Chapter[]; current?: number; onselect: (c: Chapter) => void } = $props();

  const PAGE = 20;
  const eink = $derived(themeState.theme === "eink");
  let page = $state(1);
  // Open on the page holding the current chapter.
  $effect(() => {
    if (current >= 0) page = Math.floor(current / PAGE) + 1;
  });
  const shown = $derived(eink ? chapters.slice((page - 1) * PAGE, page * PAGE) : chapters);
</script>

<ol class:scroll={!eink}>
  {#each shown as c (c.index)}
    <li>
      <button type="button" aria-current={c.index === chapters[current]?.index ? "true" : undefined} onclick={() => onselect(c)}>
        <span class="title">{c.title}</span>
        <span class="muted time">
          <span class="sr-only">{t("chapters.starts_at")}</span>{clock(c.start_ms)} · {duration(c.end_ms - c.start_ms)}
        </span>
      </button>
    </li>
  {/each}
</ol>
{#if eink && chapters.length > PAGE}
  <Pager {page} hasNext={page * PAGE < chapters.length} onprev={() => page--} onnext={() => page++} />
{/if}

<style>
  ol {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .scroll {
    max-height: 60vh;
    overflow-y: auto;
  }
  li + li {
    border-top: 1px solid var(--border);
  }
  button {
    width: 100%;
    min-height: 48px;
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 1rem;
    padding: 0.4rem 0.5rem;
    background: transparent;
    border: 0;
    color: var(--text);
    font: inherit;
    text-align: start;
    cursor: pointer;
  }
  button:hover {
    background: var(--surface-2);
  }
  [aria-current="true"] {
    font-weight: 700;
    border-inline-start: 4px solid var(--accent);
  }
  .time {
    white-space: nowrap;
    font-size: 0.875rem;
  }
</style>
