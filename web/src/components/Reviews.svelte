<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Ratings and reviews of one book: the average, your own review, everyone
  // else's, and report / moderator controls.
  import { api, type Schemas } from "../api/client";
  import { ago } from "../lib/format";
  import { t, tError } from "../lib/i18n";
  import { session } from "../lib/session.svelte";
  import Alert from "./Alert.svelte";
  import Button from "./Button.svelte";
  import Dialog from "./Dialog.svelte";
  import Field from "./Field.svelte";
  import ReportDialog, { type ReportTarget } from "./ReportDialog.svelte";
  import Stars from "./Stars.svelte";

  type Review = Schemas["Review"];
  const MAX = 500;

  let { bookId }: { bookId: string } = $props();

  let items = $state<Review[]>([]);
  let summary = $state<Schemas["RatingSummary"] | null>(null);
  let mine = $state<Review | null>(null);
  let next = $state<string | undefined>();
  let error = $state("");
  let busy = $state(false);

  const canReview = $derived(!!session.me?.permissions.can_review || session.me?.role === "admin");
  const isAdmin = $derived(session.me?.role === "admin");

  // Your review form.
  let stars = $state(0);
  let text = $state("");
  let editing = $state(false);
  let saved = $state("");

  async function load(cursor?: string) {
    const { data, error: err } = await api.GET("/books/{bookId}/reviews", {
      params: { path: { bookId }, query: { cursor, limit: 20 } },
    });
    if (err || !data) {
      error = tError(err?.code, err?.message);
      return;
    }
    error = "";
    items = cursor ? [...items, ...data.items] : data.items;
    summary = data.summary;
    mine = data.mine ?? null;
    next = data.next_cursor;
    if (!cursor && !editing) {
      stars = mine?.stars ?? 0;
      text = mine?.text ?? "";
    }
  }

  $effect(() => {
    void load();
  });

  async function save(e: SubmitEvent) {
    e.preventDefault();
    if (!stars) return;
    busy = true;
    saved = "";
    const { error: err } = await api.PUT("/books/{bookId}/reviews/mine", {
      params: { path: { bookId } },
      body: { stars, text: text.trim() || undefined },
    });
    busy = false;
    if (err) {
      error = tError(err.code, err.message);
      return;
    }
    editing = false;
    saved = t("reviews.saved");
    await load();
  }

  let deleteOpen = $state(false);
  async function remove() {
    busy = true;
    const { error: err } = await api.DELETE("/books/{bookId}/reviews/mine", { params: { path: { bookId } } });
    busy = false;
    deleteOpen = false;
    if (err) error = tError(err.code, err.message);
    editing = false;
    stars = 0;
    text = "";
    saved = "";
    await load();
  }

  // Reporting.
  let reportTargets = $state<ReportTarget[] | null>(null);
  function report(r: Review) {
    const targets: ReportTarget[] = [{ kind: "review", id: r.id, label: r.author?.display_name ?? t("reviews.former_user") }];
    if (r.author) targets.push({ kind: "user", id: r.author.id, label: r.author.display_name });
    reportTargets = targets;
  }

  // Moderation (admins).
  let hiding = $state<Review | null>(null);
  let hideReason = $state("");
  async function hide(e: SubmitEvent) {
    e.preventDefault();
    if (!hiding || !hideReason.trim()) return;
    busy = true;
    const { error: err } = await api.POST("/admin/reviews/{reviewId}/hide", {
      params: { path: { reviewId: hiding.id } },
      body: { reason: hideReason.trim() },
    });
    busy = false;
    hiding = null;
    hideReason = "";
    if (err) error = tError(err.code, err.message);
    await load();
  }
  async function unhide(r: Review) {
    busy = true;
    const { error: err } = await api.DELETE("/admin/reviews/{reviewId}/hide", { params: { path: { reviewId: r.id } } });
    busy = false;
    if (err) error = tError(err.code, err.message);
    await load();
  }

  const others = $derived(items.filter((r) => !r.mine));
  const maxBar = $derived(Math.max(1, ...(summary?.histogram ?? [0])));
  const showForm = $derived(canReview && (!mine || editing));
</script>

<div class="reviews stack">
  {#if summary && summary.count > 0}
    <div class="summary">
      <div class="avg">
        <span class="big">{summary.average.toFixed(1)}</span>
        <Stars value={summary.average} size="1.25rem" />
        <span class="muted">{t("reviews.count", { n: summary.count })}</span>
      </div>
      <ul class="hist" aria-label={t("reviews.breakdown")}>
        {#each [5, 4, 3, 2, 1] as n (n)}
          {@const c = summary.histogram[n - 1] ?? 0}
          <li>
            <span class="lbl">{t("reviews.stars", { n })}</span>
            <span class="bar" aria-hidden="true"><span style:width="{(c / maxBar) * 100}%"></span></span>
            <span class="num">{c}</span>
          </li>
        {/each}
      </ul>
    </div>
  {:else if summary}
    <p class="muted">{t("reviews.none")}</p>
  {/if}

  {#if error}<Alert kind="error">{error}</Alert>{/if}
  {#if saved}<Alert kind="ok">{saved}</Alert>{/if}

  {#if mine && !editing}
    <div class="card stack mine">
      <div class="row head">
        <strong>{t("reviews.yours")}</strong>
        <Stars value={mine.stars} />
        {#if mine.hidden}<span class="pill">{t("reviews.hidden")}</span>{/if}
      </div>
      {#if mine.hidden}<p class="muted">{t("reviews.hidden_mine")}</p>{/if}
      {#if mine.text}<p class="text">{mine.text}</p>{/if}
      <div class="row">
        {#if canReview}
          <Button
            variant="secondary"
            onclick={() => {
              stars = mine?.stars ?? 0;
              text = mine?.text ?? "";
              saved = "";
              editing = true;
            }}>{t("reviews.edit")}</Button
          >
        {/if}
        <Button variant="ghost" onclick={() => (deleteOpen = true)}>{t("reviews.delete")}</Button>
      </div>
    </div>
  {:else if showForm}
    <form class="card stack" onsubmit={save}>
      <Stars input bind:value={stars} />
      <Field
        label={t("reviews.text")}
        bind:value={text}
        multiline
        rows={3}
        hint={t("reviews.text_hint", { left: MAX - [...text].length })}
        error={[...text].length > MAX ? t("reviews.too_long") : ""}
      />
      <div class="row">
        <Button type="submit" {busy} disabled={!stars || [...text].length > MAX}>{t(mine ? "reviews.update" : "reviews.post")}</Button>
        {#if editing}<Button variant="secondary" onclick={() => (editing = false)}>{t("common.cancel")}</Button>{/if}
      </div>
    </form>
  {/if}

  {#if others.length}
    <ul class="list">
      {#each others as r (r.id)}
        <li class="stack">
          <div class="row head">
            <strong>{r.author?.display_name ?? t("reviews.former_user")}</strong>
            <Stars value={r.stars} />
            <span class="muted">{ago(r.updated_at)}</span>
            {#if r.hidden}<span class="pill">{t("reviews.hidden")}</span>{/if}
          </div>
          {#if r.hidden && r.hidden_reason}<p class="muted">{t("reviews.hidden_reason", { reason: r.hidden_reason })}</p>{/if}
          {#if r.text}<p class="text">{r.text}</p>{/if}
          <div class="row actions">
            <Button variant="ghost" onclick={() => report(r)}>{t("report.action")}</Button>
            {#if isAdmin}
              {#if r.hidden}
                <Button variant="ghost" {busy} onclick={() => unhide(r)}>{t("reviews.unhide")}</Button>
              {:else}
                <Button variant="ghost" onclick={() => (hiding = r)}>{t("reviews.hide")}</Button>
              {/if}
            {/if}
          </div>
        </li>
      {/each}
    </ul>
  {/if}
  {#if next}
    <div><Button variant="secondary" onclick={() => load(next)}>{t("reviews.more")}</Button></div>
  {/if}
</div>

<ReportDialog open={!!reportTargets} targets={reportTargets ?? []} onclose={() => (reportTargets = null)} />

<Dialog open={deleteOpen} title={t("reviews.delete_title")} onclose={() => (deleteOpen = false)}>
  <p>{t("reviews.delete_body")}</p>
  <div class="row">
    <Button variant="danger" {busy} onclick={remove}>{t("reviews.delete")}</Button>
    <Button variant="secondary" onclick={() => (deleteOpen = false)}>{t("common.cancel")}</Button>
  </div>
</Dialog>

<Dialog open={!!hiding} title={t("reviews.hide_title")} onclose={() => (hiding = null)}>
  <form class="stack" onsubmit={hide}>
    <p>{t("reviews.hide_body")}</p>
    <Field label={t("admin.reason")} bind:value={hideReason} required multiline rows={2} />
    <div class="row">
      <Button variant="danger" type="submit" {busy} disabled={!hideReason.trim()}>{t("reviews.hide")}</Button>
      <Button variant="secondary" onclick={() => (hiding = null)}>{t("common.cancel")}</Button>
    </div>
  </form>
</Dialog>

<style>
  .summary {
    display: flex;
    flex-wrap: wrap;
    gap: 1.5rem 2.5rem;
    align-items: center;
  }
  .avg {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 0.25rem;
  }
  .big {
    font-size: 2.5rem;
    font-weight: 700;
    line-height: 1;
  }
  .hist {
    list-style: none;
    margin: 0;
    padding: 0;
    flex: 1 1 14rem;
    max-width: 22rem;
  }
  .hist li {
    display: grid;
    grid-template-columns: 4.5rem 1fr 2.5rem;
    gap: 0.5rem;
    align-items: center;
    min-height: 1.6rem;
    font-size: 0.9rem;
  }
  .bar {
    height: 8px;
    border-radius: 4px;
    background: var(--surface-2);
    overflow: hidden;
  }
  .bar span {
    display: block;
    height: 100%;
    background: var(--accent);
    border-radius: 4px;
  }
  .num {
    text-align: end;
    font-variant-numeric: tabular-nums;
  }
  .head {
    gap: 0.6rem;
  }
  .text {
    margin: 0;
    white-space: pre-line;
    max-width: 65ch;
  }
  .mine p {
    margin: 0;
  }
  .list {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .list li {
    padding: 0.85rem 0;
    gap: 0.4rem;
  }
  .list li + li {
    border-top: 1px solid var(--border);
  }
  .list p {
    margin: 0;
  }
  .actions {
    gap: 0.25rem;
  }
  @media (forced-colors: active) {
    .bar {
      border: 1px solid CanvasText;
    }
    .bar span {
      background: CanvasText;
    }
  }
</style>
