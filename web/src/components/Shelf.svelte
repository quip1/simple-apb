<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // A Home row. Scrolls sideways in the dark theme; wraps into a grid in e-ink
  // (no smooth scrolling there, brief §11).
  import type { BookSummary } from "../api/client";
  import { t } from "../lib/i18n";
  import { link } from "../lib/router.svelte";
  import BookCard from "./BookCard.svelte";

  let { id, title, books, more }: { id: string; title: string; books: BookSummary[]; more?: string } = $props();
</script>

<section aria-labelledby={id}>
  <div class="head">
    <h2 {id}>{title}</h2>
    {#if more}<a href={more} use:link>{t("home.see_all")}</a>{/if}
  </div>
  <ul>
    {#each books as book (book.id)}
      <li><BookCard {book} /></li>
    {/each}
  </ul>
</section>

<style>
  .head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: 1rem;
  }
  .head a {
    min-height: 48px;
    display: inline-flex;
    align-items: center;
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0 0 0.5rem;
    display: grid;
    grid-auto-flow: column;
    grid-auto-columns: minmax(8.5rem, 10rem);
    gap: 1rem;
    overflow-x: auto;
    scroll-snap-type: x proximity;
  }
  li {
    scroll-snap-align: start;
  }
  :global([data-theme="eink"]) ul {
    grid-auto-flow: row;
    grid-template-columns: repeat(auto-fill, minmax(8.5rem, 1fr));
    overflow: visible;
  }
</style>
