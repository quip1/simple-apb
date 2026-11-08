<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Author, narrator and series pages: the books of theirs I can see.
  import { api, type BookSummary } from "../api/client";
  import Alert from "../components/Alert.svelte";
  import BookGrid from "../components/BookGrid.svelte";
  import { t, tError } from "../lib/i18n";
  import { live } from "../lib/live.svelte";
  import { prefs } from "../lib/prefs.svelte";
  import { session } from "../lib/session.svelte";

  let { kind, id }: { kind: "author" | "narrator" | "series"; id: string } = $props();

  let name = $state("");
  let books = $state<BookSummary[]>([]);
  let error = $state("");
  let loaded = $state(false);

  async function load(kind: string, id: string) {
    loaded = false;
    const res =
      kind === "author"
        ? await api.GET("/authors/{personId}", { params: { path: { personId: id } } })
        : kind === "narrator"
          ? await api.GET("/narrators/{personId}", { params: { path: { personId: id } } })
          : await api.GET("/series/{seriesId}", { params: { path: { seriesId: id } } });
    loaded = true;
    if (res.error || !res.data) {
      error = tError(res.error?.code, res.error?.message);
      return;
    }
    error = "";
    name = res.data.name;
    books = res.data.books;
  }

  $effect(() => {
    void live.tick;
    void load(kind, id);
  });
</script>

<svelte:head><title>{name || t("common.loading")} · {session.instance?.name ?? ""}</title></svelte:head>

<div class="stack">
  <p class="muted kind">{t(`person.kind_${kind}`)}</p>
  <h1>{name}</h1>
  {#if error}
    <Alert kind="error">{error}</Alert>
  {:else if !loaded}
    <p class="muted" role="status">{t("common.loading")}</p>
  {:else}
    <p class="muted">{t("library.book_count", { n: books.length })}</p>
    <!-- A series reads best as an ordered list with positions. -->
    <BookGrid {books} layout={kind === "series" ? "list" : prefs.libraryView} />
  {/if}
</div>

<style>
  .kind {
    margin: 0;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    font-size: 0.8rem;
  }
</style>
