<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Home: Continue listening, Recently added, Next in series (brief §7.4/§9).
  import { api, type BookSummary } from "../api/client";
  import Alert from "../components/Alert.svelte";
  import Shelf from "../components/Shelf.svelte";
  import { t } from "../lib/i18n";
  import { live } from "../lib/live.svelte";
  import { link } from "../lib/router.svelte";
  import { session } from "../lib/session.svelte";

  let loaded = $state(false);
  let failed = $state(false);
  let continuing = $state<BookSummary[]>([]);
  let recent = $state<BookSummary[]>([]);
  let nextUp = $state<BookSummary[]>([]);

  const ROW = 12;

  async function shelf(shelf: "in_progress" | "all" | "finished", sort: "recent_played" | "recent_added", limit = ROW) {
    const { data, error } = await api.GET("/library", { params: { query: { shelf, sort, limit } } });
    if (error) throw new Error(error.code);
    return data.items;
  }

  // The next unstarted book in each series you've finished a book of.
  async function nextInSeries(finished: BookSummary[]): Promise<BookSummary[]> {
    const seen = new Set<string>();
    const series = finished
      .map((b) => b.series)
      .filter((s): s is NonNullable<typeof s> => !!s && !seen.has(s.id) && !!seen.add(s.id))
      .slice(0, 6);
    const pages = await Promise.all(series.map((s) => api.GET("/series/{seriesId}", { params: { path: { seriesId: s.id } } })));
    const out: BookSummary[] = [];
    for (const p of pages) {
      const books = p.data?.books ?? [];
      let lastFinished = -1;
      books.forEach((b, i) => {
        if (b.progress?.finished_at) lastFinished = i;
      });
      const next = books.slice(lastFinished + 1).find((b) => !b.progress?.finished_at && !(b.progress?.position_ms ?? 0));
      if (next) out.push(next);
    }
    return out;
  }

  async function load() {
    try {
      const [c, r, f] = await Promise.all([
        shelf("in_progress", "recent_played"),
        shelf("all", "recent_added"),
        shelf("finished", "recent_played", 20),
      ]);
      continuing = c;
      recent = r;
      nextUp = await nextInSeries(f);
      failed = false;
    } catch {
      failed = true;
    }
    loaded = true;
  }

  $effect(() => {
    void live.tick;
    void load();
  });

  const empty = $derived(loaded && !failed && !continuing.length && !recent.length && !nextUp.length);
</script>

<svelte:head><title>{t("nav.home")} · {session.instance?.name ?? ""}</title></svelte:head>

<div class="stack home">
  <h1>{t("home.welcome", { name: session.me?.display_name ?? "" })}</h1>
  {#if failed}
    <Alert kind="error">{t("error.generic")}</Alert>
  {/if}
  {#if !loaded}
    <p class="muted" role="status">{t("common.loading")}</p>
  {:else if empty}
    <div class="empty card stack">
      <h2>{t("home.empty_title")}</h2>
      <p class="muted">{t("home.empty_body")}</p>
    </div>
  {:else}
    {#if continuing.length}
      <Shelf id="shelf-continue" title={t("home.continue")} books={continuing} more="/library?shelf=in_progress" />
    {/if}
    {#if nextUp.length}
      <Shelf id="shelf-next" title={t("home.next_in_series")} books={nextUp} />
    {/if}
    {#if recent.length}
      <Shelf id="shelf-recent" title={t("home.recently_added")} books={recent} more="/library" />
    {/if}
    <p><a href="/library" use:link>{t("home.browse_library")}</a></p>
  {/if}
</div>

<style>
  .home > :global(section) {
    margin-top: 2rem;
  }
  .empty {
    max-width: 36rem;
  }
</style>
