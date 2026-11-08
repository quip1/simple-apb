<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Listening stats and year in review (brief §6.17, §7.4): headline numbers,
  // daily (last 30 days) and weekly charts, time per book, top authors and
  // books finished. The longest streak is a fun fact only.
  import { api, type Schemas } from "../api/client";
  import Alert from "../components/Alert.svelte";
  import BarChart, { type Bar } from "../components/BarChart.svelte";
  import Cover from "../components/Cover.svelte";
  import ProgressBar from "../components/ProgressBar.svelte";
  import { hours } from "../lib/format";
  import { t, tError } from "../lib/i18n";
  import { live } from "../lib/live.svelte";
  import { link } from "../lib/router.svelte";
  import { session } from "../lib/session.svelte";

  type Stats = Schemas["MyStats"];

  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  const thisYear = new Date().getFullYear();
  const locale = document.documentElement.lang || "en";

  let year = $state(thisYear);
  let data = $state<Stats | null>(null);
  let error = $state("");

  async function load(y: number) {
    const { data: d, error: err } = await api.GET("/me/stats", { params: { query: { year: y, tz } } });
    if (err || !d) {
      error = tError(err?.code, err?.message);
      return;
    }
    error = "";
    data = d;
  }

  $effect(() => {
    void live.tick;
    void load(year);
  });

  const years = $derived([...new Set([thisYear, ...(data?.years ?? [])])].sort((a, b) => b - a));

  // Local calendar dates (the server buckets days in our time zone).
  function key(d: Date): string {
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  }
  const byDay = $derived(new Map((data?.days ?? []).map((d) => [d.date, d.listened_ms])));
  const lastDay = $derived(year === thisYear ? new Date() : new Date(year, 11, 31));

  const daily = $derived.by((): Bar[] => {
    const fmt = new Intl.DateTimeFormat(locale, { weekday: "short", day: "numeric", month: "short" });
    const out: Bar[] = [];
    for (let i = 29; i >= 0; i--) {
      const d = new Date(lastDay.getFullYear(), lastDay.getMonth(), lastDay.getDate() - i);
      if (d.getFullYear() !== year) continue;
      out.push({ label: fmt.format(d), tick: i % 7 === 0 ? String(d.getDate()) : "", value: byDay.get(key(d)) ?? 0 });
    }
    return out;
  });

  const weekly = $derived.by((): Bar[] => {
    const fmt = new Intl.DateTimeFormat(locale, { day: "numeric", month: "short" });
    const month = new Intl.DateTimeFormat(locale, { month: "short" });
    const out: Bar[] = [];
    const jan1 = new Date(year, 0, 1);
    // Weeks start on Monday.
    let start = new Date(year, 0, 1 - ((jan1.getDay() + 6) % 7));
    while (start <= lastDay) {
      let sum = 0;
      let tick = "";
      for (let i = 0; i < 7; i++) {
        const d = new Date(start.getFullYear(), start.getMonth(), start.getDate() + i);
        if (d.getFullYear() !== year) continue;
        sum += byDay.get(key(d)) ?? 0;
        if (d.getDate() === 1) tick = month.format(d);
      }
      out.push({ label: t("stats.week_of", { date: fmt.format(start) }), tick, value: sum });
      start = new Date(start.getFullYear(), start.getMonth(), start.getDate() + 7);
    }
    return out;
  });

  const topBookMs = $derived(data?.books[0]?.listened_ms ?? 0);
  const dateFmt = new Intl.DateTimeFormat(locale, { day: "numeric", month: "long" });
</script>

<svelte:head><title>{t("nav.stats")} · {session.instance?.name ?? ""}</title></svelte:head>

<div class="stack">
  <div class="row top">
    <h1>{t("stats.title", { year })}</h1>
    <label>
      <span class="sr-only">{t("stats.year")}</span>
      <select value={year} onchange={(e) => (year = Number(e.currentTarget.value))}>
        {#each years as y (y)}<option value={y}>{y}</option>{/each}
      </select>
    </label>
  </div>

  {#if error}<Alert kind="error">{error}</Alert>{/if}

  {#if !data}
    <p class="muted" role="status">{t("common.loading")}</p>
  {:else if data.total_ms === 0 && data.books_finished === 0}
    <div class="card stack">
      <h2>{t("stats.empty_title")}</h2>
      <p class="muted">{t("stats.empty_body")}</p>
    </div>
  {:else}
    <section aria-labelledby="st-review">
      <h2 id="st-review" class="sr-only">{t("stats.review", { year })}</h2>
      <dl class="tiles">
        <div class="tile">
          <dt>{t("stats.total")}</dt>
          <dd>{hours(data.total_ms)}</dd>
        </div>
        <div class="tile">
          <dt>{t("stats.finished")}</dt>
          <dd>{data.books_finished}</dd>
        </div>
        <div class="tile">
          <dt>{t("stats.active_days")}</dt>
          <dd>{data.active_days}</dd>
        </div>
        <div class="tile">
          <dt>{t("stats.streak")}</dt>
          <dd>{t("stats.days", { n: data.longest_streak_days })}</dd>
        </div>
      </dl>
      {#if data.books[0]}
        <p class="fact">
          {t("stats.top_fact", {
            title: data.books[0].title,
            time: hours(data.books[0].listened_ms),
            author: data.authors[0]?.name ?? "",
          })}
        </p>
      {/if}
    </section>

    <section class="card stack">
      <BarChart title={t("stats.daily")} bars={daily} format={hours} />
    </section>
    <section class="card stack">
      <BarChart title={t("stats.weekly")} bars={weekly} format={hours} />
    </section>

    <div class="cols">
      <section class="card stack" aria-labelledby="st-books">
        <h2 id="st-books">{t("stats.per_book")}</h2>
        <ol class="ranked">
          {#each data.books as b (b.book_id)}
            <li>
              <div class="art"><Cover src={b.cover_url} has={b.has_cover} title={b.title} size={128} /></div>
              <div class="grow">
                <a href="/books/{b.book_id}" use:link>{b.title}</a>
                <div class="meter">
                  <ProgressBar value={topBookMs ? Math.round((b.listened_ms / topBookMs) * 100) : 0} label={hours(b.listened_ms)} />
                  <span class="muted">{hours(b.listened_ms)}</span>
                </div>
              </div>
            </li>
          {/each}
        </ol>
      </section>

      <div class="stack">
        <section class="card stack" aria-labelledby="st-authors">
          <h2 id="st-authors">{t("stats.top_authors")}</h2>
          <ol class="plain">
            {#each data.authors as a (a.id)}
              <li><a href="/authors/{a.id}" use:link>{a.name}</a><span class="muted">{hours(a.listened_ms)}</span></li>
            {/each}
          </ol>
        </section>

        <section class="card stack" aria-labelledby="st-finished">
          <h2 id="st-finished">{t("stats.finished_list")}</h2>
          {#if data.finished.length}
            <ul class="plain">
              {#each data.finished as b (b.book_id)}
                <li>
                  <a href="/books/{b.book_id}" use:link>{b.title}</a>
                  <span class="muted">{b.finished_at ? dateFmt.format(new Date(b.finished_at)) : ""}</span>
                </li>
              {/each}
            </ul>
          {:else}
            <p class="muted">{t("stats.none_finished")}</p>
          {/if}
        </section>
      </div>
    </div>
  {/if}
</div>

<style>
  .top {
    justify-content: space-between;
  }
  .top h1 {
    margin: 0;
  }
  .tiles {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(9rem, 1fr));
    gap: 1rem;
    margin: 0;
  }
  .tile {
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    padding: 1rem;
  }
  .tile dt {
    color: var(--text-muted);
    font-size: 0.875rem;
  }
  .tile dd {
    margin: 0.25rem 0 0;
    font-size: 1.6rem;
    font-weight: 700;
    font-variant-numeric: tabular-nums;
  }
  .fact {
    margin-top: 1rem;
  }
  .cols {
    display: grid;
    grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
    gap: 1rem;
    align-items: start;
  }
  .ranked,
  .plain {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .ranked li {
    display: flex;
    gap: 0.75rem;
    align-items: center;
    padding: 0.4rem 0;
  }
  .art {
    width: 3rem;
    flex: none;
  }
  .grow {
    flex: 1;
    min-width: 0;
  }
  .meter {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .meter :global(.bar) {
    flex: 1;
  }
  .meter span {
    font-size: 0.85rem;
    white-space: nowrap;
  }
  .plain li {
    display: flex;
    justify-content: space-between;
    gap: 1rem;
    min-height: 40px;
    align-items: center;
  }
  .plain li + li {
    border-top: 1px solid var(--border);
  }
  @media (max-width: 48rem) {
    .cols {
      grid-template-columns: 1fr;
    }
  }
</style>
