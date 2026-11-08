<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import type { BookSummary } from "../api/client";
  import BookCard from "./BookCard.svelte";

  let { books, layout = "grid" }: { books: BookSummary[]; layout?: "grid" | "list" } = $props();
</script>

<ul class={layout}>
  {#each books as book (book.id)}
    <li><BookCard {book} {layout} /></li>
  {/each}
</ul>

<style>
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(9rem, 1fr));
    gap: 1.25rem 1rem;
  }
  .list li + li {
    border-top: 1px solid var(--border);
  }
</style>
