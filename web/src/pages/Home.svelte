<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Home: Continue listening, Recommended to you, Next in series, Recently
  // added (brief §7.4/§9).
  import { api, type BookSummary, type Schemas } from "../api/client";
  import Alert from "../components/Alert.svelte";
  import Button from "../components/Button.svelte";
  import NowListening from "../components/NowListening.svelte";
  import Shelf from "../components/Shelf.svelte";
  import { t } from "../lib/i18n";
  import { live } from "../lib/live.svelte";
  import { player } from "../lib/player.svelte";
  import { link } from "../lib/router.svelte";
  import { session } from "../lib/session.svelte";

  let loaded = $state(false);
  let failed = $state(false);
  let continuing = $state<BookSummary[]>([]);
  let recent = $state<BookSummary[]>([]);
  let nextUp = $state<BookSummary[]>([]);
  let recs = $state<Schemas["Recommendation"][]>([]);
  const recFor = $derived(new Map(recs.map((r) => [r.book.id, r])));

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
      const [c, r, f, rec] = await Promise.all([
        shelf("in_progress", "recent_played"),
        shelf("all", "recent_added"),
        shelf("finished", "recent_played", 20),
        api.GET("/me/recommendations"),
      ]);
      continuing = c;
      recent = r;
      // One card per book, newest recommendation first.
      const seen = new Set<string>();
      recs = (rec.data ?? []).filter((x) => !seen.has(x.book.id) && !!seen.add(x.book.id));
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

  const empty = $derived(loaded && !failed && !continuing.length && !recent.length && !nextUp.length && !recs.length);

  async function dismiss(bookId: string) {
    const all = (await api.GET("/me/recommendations")).data ?? [];
    await Promise.all(
      all
        .filter((r) => r.book.id === bookId)
        .map((r) => api.DELETE("/me/recommendations/{recommendationId}", { params: { path: { recommendationId: r.id } } })),
    );
    recs = recs.filter((r) => r.book.id !== bookId);
  }
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
    <!-- The book you're on, front and centre (ADR-0051); the row holds the others. -->
    <NowListening fallback={continuing[0]} />
    {@const current = player.book?.id ?? continuing[0]?.id}
    {@const others = continuing.filter((b) => b.id !== current)}
    {#if others.length}
      <Shelf id="shelf-continue" title={t("home.continue")} books={others} more="/library?shelf=in_progress" />
    {/if}
    {#if recs.length}
      <Shelf id="shelf-recs" title={t("home.recommended")} books={recs.map((r) => r.book)}>
        {#snippet below(book: BookSummary)}
          {@const r = recFor.get(book.id)}
          {#if r}
            <div class="rec">
              <p class="muted">{t("home.recommended_by", { name: r.from.display_name })}</p>
              {#if r.note}<p class="note">“{r.note}”</p>{/if}
              <Button variant="ghost" onclick={() => dismiss(book.id)}>{t("home.dismiss")}</Button>
            </div>
          {/if}
        {/snippet}
      </Shelf>
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
  .rec p {
    margin: 0.25rem 0 0;
    font-size: 0.9rem;
    overflow-wrap: anywhere;
  }
  .rec .note {
    font-style: italic;
  }
</style>
