<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Library (brief §7.4): views All/Series/Authors/Narrators/Genres/Collections,
  // shelves, grid or list, sort, instant search. The state lives in the URL so
  // Back and deep links work. E-ink uses explicit pages instead of "load more".
  import { api, type BookSummary, type Schemas } from "../api/client";
  import Alert from "../components/Alert.svelte";
  import BookGrid from "../components/BookGrid.svelte";
  import Button from "../components/Button.svelte";
  import Dialog from "../components/Dialog.svelte";
  import Icon from "../components/Icon.svelte";
  import IconButton from "../components/IconButton.svelte";
  import Pager from "../components/Pager.svelte";
  import ShareDialog from "../components/ShareDialog.svelte";
  import { t, tError } from "../lib/i18n";
  import { live } from "../lib/live.svelte";
  import { prefs, savePrefs } from "../lib/prefs.svelte";
  import { link, navigate, route } from "../lib/router.svelte";
  import { session } from "../lib/session.svelte";
  import { themeState } from "../lib/theme.svelte";

  type View = "books" | "series" | "authors" | "narrators" | "genres" | "collections";
  type Shelf = "all" | "in_progress" | "not_started" | "finished" | "archived" | "recently_removed";
  type Sort = "recent_added" | "recent_played" | "title" | "author" | "series" | "duration";
  type Facet = Schemas["LibraryFacet"];
  type Collection = Schemas["Collection"];

  const VIEWS: View[] = ["books", "series", "authors", "narrators", "genres", "collections"];
  const SHELVES: Shelf[] = ["all", "in_progress", "not_started", "finished", "archived", "recently_removed"];
  const SORTS: Sort[] = ["recent_added", "recent_played", "title", "author", "series", "duration"];
  const FACET_PAGE = 30;

  const eink = $derived(themeState.theme === "eink");
  const pageSize = $derived(eink ? 24 : 48);

  const view = $derived((VIEWS as string[]).includes(route.query.get("view") ?? "") ? (route.query.get("view") as View) : "books");
  const shelf = $derived((SHELVES as string[]).includes(route.query.get("shelf") ?? "") ? (route.query.get("shelf") as Shelf) : "all");
  const sort = $derived((SORTS as string[]).includes(route.query.get("sort") ?? "") ? (route.query.get("sort") as Sort) : "recent_added");
  const query = $derived(route.query.get("q") ?? "");
  const tag = $derived(route.query.get("tag") ?? "");
  const collection = $derived(route.query.get("collection") ?? "");

  /** Update the URL query (replacing history for typing, pushing otherwise). */
  function setQuery(changes: Record<string, string | undefined>, replace = false) {
    const next = new URLSearchParams(route.query);
    for (const [k, v] of Object.entries(changes)) {
      if (v === undefined || v === "") next.delete(k);
      else next.set(k, v);
    }
    const qs = next.toString();
    navigate(`/library${qs ? `?${qs}` : ""}`, { replace });
  }

  // ── search box (debounced into the URL) ──
  let search = $state(route.query.get("q") ?? "");
  let searchTimer: ReturnType<typeof setTimeout> | undefined;
  function onSearch() {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => setQuery({ q: search.trim() || undefined, view: undefined }, true), 250);
  }

  // ── books ──
  let books = $state<BookSummary[]>([]);
  let nextCursor = $state<string | undefined>();
  let cursors = $state<(string | undefined)[]>([undefined]);
  let page = $state(1);
  let loading = $state(false);
  let error = $state("");
  let seq = 0;

  async function fetchBooks(cursor: string | undefined, append: boolean) {
    const mine = ++seq;
    loading = true;
    const { data, error: err } = await api.GET("/library", {
      params: {
        query: {
          shelf,
          sort,
          q: query || undefined,
          tag: tag || undefined,
          collection: collection || undefined,
          cursor,
          limit: pageSize,
        },
      },
    });
    if (mine !== seq) return;
    loading = false;
    if (err || !data) {
      error = tError(err?.code, err?.message);
      return;
    }
    error = "";
    books = append ? [...books, ...data.items] : data.items;
    nextCursor = data.next_cursor;
  }

  $effect(() => {
    if (view !== "books") return;
    // Re-run when any filter, the page size or a sync hint changes.
    void [shelf, sort, query, tag, collection, pageSize, live.tick];
    page = 1;
    cursors = [undefined];
    void fetchBooks(undefined, false);
  });

  function loadMore() {
    if (nextCursor && !loading) void fetchBooks(nextCursor, true);
  }
  function nextPage() {
    if (!nextCursor) return;
    cursors = [...cursors.slice(0, page), nextCursor];
    page++;
    void fetchBooks(nextCursor, false);
    window.scrollTo(0, 0);
  }
  function prevPage() {
    if (page <= 1) return;
    page--;
    void fetchBooks(cursors[page - 1], false);
    window.scrollTo(0, 0);
  }

  // Infinite scroll in the dark theme (a button is the fallback).
  let sentinel: HTMLElement | undefined = $state();
  $effect(() => {
    if (!sentinel || eink) return;
    const io = new IntersectionObserver((es) => es.some((e) => e.isIntersecting) && loadMore(), {
      rootMargin: "600px",
    });
    io.observe(sentinel);
    return () => io.disconnect();
  });

  // ── facets ──
  let facets = $state<Facet[]>([]);
  let facetPage = $state(1);
  const facetKind = $derived(view === "genres" ? "tags" : view === "series" || view === "authors" || view === "narrators" ? view : null);
  $effect(() => {
    const kind = facetKind;
    void live.tick;
    if (!kind) return;
    facetPage = 1;
    facets = [];
    loading = true;
    void api.GET("/library/facets", { params: { query: { kind } } }).then(({ data, error: err }) => {
      loading = false;
      if (err || !data) error = tError(err?.code, err?.message);
      else {
        error = "";
        facets = data;
      }
    });
  });
  const shownFacets = $derived(eink ? facets.slice((facetPage - 1) * FACET_PAGE, facetPage * FACET_PAGE) : facets);
  function facetHref(f: Facet): string {
    if (view === "series") return `/series/${f.id}`;
    if (view === "authors") return `/authors/${f.id}`;
    if (view === "narrators") return `/narrators/${f.id}`;
    return `/library?tag=${f.id}`;
  }

  // Name of the active genre filter.
  let tagName = $state("");
  $effect(() => {
    tagName = "";
    if (!tag) return;
    void api.GET("/library/facets", { params: { query: { kind: "tags" } } }).then(({ data }) => {
      tagName = data?.find((f) => f.id === tag)?.name ?? "";
    });
  });

  // ── collections ──
  let collections = $state<Collection[]>([]);
  let shared = $state<Schemas["SharedCollection"][]>([]);
  async function loadCollections() {
    const [own, other] = await Promise.all([api.GET("/me/collections"), api.GET("/shared-collections")]);
    collections = own.data ?? [];
    shared = other.data ?? [];
  }
  let shareOpen = $state(false);
  $effect(() => {
    void live.tick;
    if (view === "collections" || collection) void loadCollections();
  });
  const activeCollection = $derived(collections.find((c) => c.id === collection));
  const activeShared = $derived(shared.find((c) => c.id === collection));

  let newName = $state("");
  let creating = $state(false);
  async function createCollection(e: SubmitEvent) {
    e.preventDefault();
    const name = newName.trim();
    if (!name) return;
    creating = true;
    const { error: err } = await api.POST("/me/collections", {
      body: { id: crypto.randomUUID(), name, client_ts: new Date().toISOString() },
    });
    creating = false;
    if (err) error = tError(err.code, err.message);
    else {
      newName = "";
      await loadCollections();
    }
  }

  let renameOpen = $state(false);
  let renameTo = $state("");
  async function renameCollection(e: SubmitEvent) {
    e.preventDefault();
    if (!activeCollection || !renameTo.trim()) return;
    await api.PATCH("/me/collections/{collectionId}", {
      params: { path: { collectionId: activeCollection.id } },
      body: { name: renameTo.trim(), client_ts: new Date().toISOString() },
    });
    renameOpen = false;
    await loadCollections();
  }
  let deleteOpen = $state(false);
  async function deleteCollection() {
    if (!activeCollection) return;
    await api.DELETE("/me/collections/{collectionId}", {
      params: { path: { collectionId: activeCollection.id } },
    });
    deleteOpen = false;
    navigate("/library?view=collections");
  }

  function setLayout(layout: "grid" | "list") {
    prefs.libraryView = layout;
    savePrefs();
  }

  const viewLabel: Record<View, string> = {
    books: "library.view_books",
    series: "library.view_series",
    authors: "library.view_authors",
    narrators: "library.view_narrators",
    genres: "library.view_genres",
    collections: "library.view_collections",
  };
</script>

<svelte:head><title>{t("nav.library")} · {session.instance?.name ?? ""}</title></svelte:head>

<div class="stack">
  <h1>{t("nav.library")}</h1>

  <nav class="views" aria-label={t("library.views")}>
    {#each VIEWS as v (v)}
      <a
        href={v === "books" ? "/library" : `/library?view=${v}`}
        use:link
        aria-current={view === v && !tag && !collection ? "page" : undefined}>{t(viewLabel[v])}</a
      >
    {/each}
  </nav>

  {#if error}<Alert kind="error">{error}</Alert>{/if}

  {#if view === "books"}
    {#if tag || collection}
      <div class="row filter">
        <h2>
          {#if tag}{t("library.genre_heading", { name: tagName || "…" })}{:else}{activeCollection?.name ?? activeShared?.name ?? "…"}{/if}
        </h2>
        {#if activeShared}
          <span class="muted">{t("library.shared_by", { name: activeShared.owner.display_name })}</span>
        {/if}
        {#if activeCollection}
          <Button variant="secondary" onclick={() => (shareOpen = true)}>{t("library.collection_share")}</Button>
          <Button
            variant="secondary"
            onclick={() => {
              renameTo = activeCollection.name;
              renameOpen = true;
            }}>{t("library.collection_rename")}</Button
          >
          <Button variant="danger" onclick={() => (deleteOpen = true)}>{t("library.collection_delete")}</Button>
        {/if}
        <a href="/library" use:link>{t("library.clear_filter")}</a>
      </div>
    {/if}

    <div class="toolbar">
      <label class="search">
        <span class="sr-only">{t("library.search")}</span>
        <Icon name="search" size={20} />
        <input
          type="search"
          bind:value={search}
          oninput={onSearch}
          placeholder={t("library.search_placeholder")}
          maxlength="200"
          autocomplete="off"
        />
      </label>
      <label class="sel">
        <span class="sr-only">{t("library.shelf")}</span>
        <select value={shelf} onchange={(e) => setQuery({ shelf: e.currentTarget.value === "all" ? undefined : e.currentTarget.value })}>
          {#each SHELVES as s (s)}<option value={s}>{t(`library.shelf_${s}`)}</option>{/each}
        </select>
      </label>
      <label class="sel">
        <span class="sr-only">{t("library.sort")}</span>
        <select
          value={sort}
          onchange={(e) => setQuery({ sort: e.currentTarget.value === "recent_added" ? undefined : e.currentTarget.value })}
        >
          {#each SORTS as s (s)}<option value={s}>{t(`library.sort_${s}`)}</option>{/each}
        </select>
      </label>
      <div class="layout" role="group" aria-label={t("library.layout")}>
        <IconButton label={t("library.layout_grid")} pressed={prefs.libraryView === "grid"} onclick={() => setLayout("grid")}>
          <Icon name="grid" />
        </IconButton>
        <IconButton label={t("library.layout_list")} pressed={prefs.libraryView === "list"} onclick={() => setLayout("list")}>
          <Icon name="list" />
        </IconButton>
      </div>
    </div>

    {#if !books.length && !loading}
      <p class="muted" role="status">{query ? t("library.no_results", { q: query }) : t("library.empty_shelf")}</p>
    {:else}
      <p class="sr-only" role="status">{loading ? t("common.loading") : t("library.count_shown", { n: books.length })}</p>
      <BookGrid {books} layout={prefs.libraryView} />
    {/if}

    {#if eink}
      {#if page > 1 || nextCursor}<Pager {page} hasNext={!!nextCursor} onprev={prevPage} onnext={nextPage} />{/if}
    {:else if nextCursor}
      <div class="more" bind:this={sentinel}>
        <Button variant="secondary" busy={loading} onclick={loadMore}>{t("library.load_more")}</Button>
      </div>
    {/if}
  {:else if view === "collections"}
    <form class="row" onsubmit={createCollection}>
      <label class="grow">
        <span class="sr-only">{t("library.collection_name")}</span>
        <input class="text" bind:value={newName} maxlength="100" placeholder={t("library.collection_new_placeholder")} />
      </label>
      <Button type="submit" busy={creating} disabled={!newName.trim()}>{t("library.collection_create")}</Button>
    </form>
    {#if collections.length}
      <ul class="facets">
        {#each collections as c (c.id)}
          <li>
            <a href="/library?collection={c.id}" use:link>
              <span>{c.name}</span>
              <span class="muted">{t("library.book_count", { n: c.book_ids.length })}</span>
            </a>
          </li>
        {/each}
      </ul>
    {:else}
      <p class="muted">{t("library.no_collections")}</p>
    {/if}
    {#if shared.length}
      <h2>{t("library.shared_with_you")}</h2>
      <ul class="facets">
        {#each shared as c (c.id)}
          <li>
            <a href="/library?collection={c.id}" use:link>
              <span>{c.name} <span class="muted">· {t("library.shared_by", { name: c.owner.display_name })}</span></span>
              <span class="muted">{t("library.book_count", { n: c.book_ids.length })}</span>
            </a>
          </li>
        {/each}
      </ul>
    {/if}
  {:else}
    {#if loading && !facets.length}
      <p class="muted" role="status">{t("common.loading")}</p>
    {:else if !facets.length}
      <p class="muted">{t("library.no_facets")}</p>
    {:else}
      <ul class="facets">
        {#each shownFacets as f (f.id)}
          <li>
            <a href={facetHref(f)} use:link>
              <span>{f.name}</span>
              <span class="muted">{t("library.book_count", { n: f.count })}</span>
            </a>
          </li>
        {/each}
      </ul>
      {#if eink && facets.length > FACET_PAGE}
        <Pager page={facetPage} hasNext={facetPage * FACET_PAGE < facets.length} onprev={() => facetPage--} onnext={() => facetPage++} />
      {/if}
    {/if}
  {/if}
</div>

<Dialog open={renameOpen} title={t("library.collection_rename")} onclose={() => (renameOpen = false)}>
  <form class="stack" onsubmit={renameCollection}>
    <label class="stack">
      <span>{t("library.collection_name")}</span>
      <input class="text" bind:value={renameTo} maxlength="100" required />
    </label>
    <div class="row">
      <Button type="submit">{t("common.save")}</Button>
      <Button variant="secondary" onclick={() => (renameOpen = false)}>{t("common.cancel")}</Button>
    </div>
  </form>
</Dialog>

{#if activeCollection}
  <ShareDialog
    open={shareOpen}
    kind="collection"
    id={activeCollection.id}
    title={activeCollection.name}
    onclose={() => (shareOpen = false)}
  />
{/if}

<Dialog open={deleteOpen} title={t("library.collection_delete")} onclose={() => (deleteOpen = false)}>
  <p>{t("library.collection_delete_body", { name: activeCollection?.name ?? "" })}</p>
  <div class="row">
    <Button variant="danger" onclick={deleteCollection}>{t("library.collection_delete")}</Button>
    <Button variant="secondary" onclick={() => (deleteOpen = false)}>{t("common.cancel")}</Button>
  </div>
</Dialog>

<style>
  .views {
    display: flex;
    gap: 0.25rem;
    overflow-x: auto;
    border-bottom: 1px solid var(--border);
  }
  .views a {
    min-height: 48px;
    display: inline-flex;
    align-items: center;
    padding: 0 0.85rem;
    color: var(--text-muted);
    text-decoration: none;
    white-space: nowrap;
    border-bottom: 3px solid transparent;
  }
  .views a[aria-current="page"] {
    color: var(--text);
    font-weight: 600;
    border-bottom-color: var(--accent);
  }
  .filter h2 {
    margin: 0;
    flex: 1;
  }
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    align-items: center;
  }
  .search {
    flex: 1 1 16rem;
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0 0.75rem;
    min-height: 48px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-2);
    color: var(--text-muted);
  }
  .search:focus-within {
    outline: 3px solid var(--focus);
    outline-offset: 2px;
  }
  .search input {
    flex: 1;
    min-width: 0;
    border: 0;
    background: transparent;
    color: var(--text);
    font: inherit;
    min-height: 46px;
  }
  .search input:focus {
    outline: none;
  }
  .layout {
    display: flex;
  }
  .more {
    display: flex;
    justify-content: center;
    margin-top: 1.5rem;
  }
  .grow {
    flex: 1 1 14rem;
  }
  .facets {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .facets li + li {
    border-top: 1px solid var(--border);
  }
  .facets a {
    display: flex;
    justify-content: space-between;
    gap: 1rem;
    min-height: 48px;
    align-items: center;
    color: var(--text);
    text-decoration: none;
    padding: 0.25rem 0.25rem;
  }
  .facets a:hover span:first-child {
    text-decoration: underline;
  }
</style>
