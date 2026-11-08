<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Inbox tab: reports about books, reviews and people (brief §6.16).
  // Resolving closes every open report about the same thing.
  import { api, type Schemas } from "../../../api/client";
  import Alert from "../../../components/Alert.svelte";
  import Button from "../../../components/Button.svelte";
  import Dialog from "../../../components/Dialog.svelte";
  import Field from "../../../components/Field.svelte";
  import Stars from "../../../components/Stars.svelte";
  import { ago } from "../../../lib/format";
  import { t, tError } from "../../../lib/i18n";
  import { live } from "../../../lib/live.svelte";
  import { link } from "../../../lib/router.svelte";

  type Report = Schemas["AdminReport"];
  type Status = Report["status"];
  type Action = Schemas["ReportResolve"]["action"];

  let { onchange }: { onchange: () => void } = $props();

  let status = $state<Status>("open");
  let items = $state<Report[]>([]);
  let next = $state<string | undefined>();
  let error = $state("");
  let busy = $state(false);

  async function load(s: Status, cursor?: string) {
    const { data, error: err } = await api.GET("/admin/reports", { params: { query: { status: s, cursor } } });
    if (err || !data) {
      error = tError(err?.code, err?.message);
      return;
    }
    items = cursor ? [...items, ...data.items] : data.items;
    next = data.next_cursor;
  }
  $effect(() => {
    void live.tick;
    void load(status);
  });

  function actionsFor(r: Report): Action[] {
    if (r.object_type === "review") return ["hide_review", "takedown", "resolved", "dismiss"];
    if (r.object_type === "book") return ["takedown", "resolved", "dismiss"];
    return ["resolved", "dismiss"];
  }

  function objectHref(r: Report): string | null {
    if (r.object_type === "user" && r.user) return `/admin/users/${r.user.id}`;
    if (r.object_type === "review" && r.book) return `/books/${r.book.id}`;
    if (r.book) return `/admin/books/${r.book.id}`;
    return null;
  }

  let resolving = $state<Report | null>(null);
  let action = $state<Action>("dismiss");
  let note = $state("");
  function open(r: Report) {
    resolving = r;
    action = actionsFor(r)[0] ?? "dismiss";
    note = "";
  }
  async function resolve(e: SubmitEvent) {
    e.preventDefault();
    if (!resolving || !note.trim()) return;
    busy = true;
    error = "";
    const { error: err } = await api.POST("/admin/reports/{reportId}/resolve", {
      params: { path: { reportId: resolving.id } },
      body: { action, note: note.trim() },
    });
    busy = false;
    resolving = null;
    if (err) error = tError(err.code, err.message);
    await load(status);
    onchange();
  }
</script>

<section class="card stack" aria-labelledby="rp-title">
  <div class="row head">
    <h2 id="rp-title">{t("admin.reports.title")}</h2>
    <label>
      <span class="sr-only">{t("admin.filter_status")}</span>
      <select bind:value={status}>
        {#each ["open", "resolved", "dismissed"] as s (s)}<option value={s}>{t(`admin.reports.${s}`)}</option>{/each}
      </select>
    </label>
  </div>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  {#if items.length}
    <ul class="items">
      {#each items as r (r.id)}
        {@const href = objectHref(r)}
        <li class="stack">
          <div class="row line">
            <span class="pill">{t(`admin.reports.kind_${r.object_type}`)}</span>
            {#if href}<a {href} use:link><strong>{r.object_label || t("admin.reports.gone")}</strong></a>
            {:else}<strong>{r.object_label || t("admin.reports.gone")}</strong>{/if}
            <span class="muted">{t(`report.reason_${r.reason}`)}</span>
          </div>
          {#if r.review}
            <blockquote>
              <div class="row line">
                <span>{r.review.author?.display_name ?? t("reviews.former_user")}</span>
                <Stars value={r.review.stars} />
                {#if r.review.hidden}<span class="pill">{t("reviews.hidden")}</span>{/if}
              </div>
              {#if r.review.text}<p>{r.review.text}</p>{/if}
            </blockquote>
          {/if}
          {#if r.details}<p class="details">{r.details}</p>{/if}
          <p class="muted">
            {t("admin.reports.by", { name: r.reporter?.display_name ?? t("reviews.former_user"), when: ago(r.created_at) })}
          </p>
          {#if r.status === "open"}
            <div class="row">
              <Button onclick={() => open(r)}>{t("admin.reports.resolve")}</Button>
              {#if r.object_type === "user" && r.user}
                <a href="/admin/users/{r.user.id}" use:link>{t("admin.reports.open_user")}</a>
              {/if}
            </div>
          {:else}
            <p>
              <span class="pill">{t(`admin.reports.action_${r.resolution ?? "resolved"}`)}</span>
              <span class="muted"
                >{t("admin.reports.closed_by", {
                  name: r.resolved_by?.display_name ?? "",
                  when: r.resolved_at ? ago(r.resolved_at) : "",
                })}</span
              >
            </p>
            {#if r.resolution_note}<p class="details">{r.resolution_note}</p>{/if}
          {/if}
        </li>
      {/each}
    </ul>
    {#if next}<div><Button variant="secondary" onclick={() => load(status, next)}>{t("reviews.more")}</Button></div>{/if}
  {:else}
    <p class="muted">{t("admin.inbox.empty")}</p>
  {/if}
</section>

<Dialog
  open={!!resolving}
  title={t("admin.reports.resolve_title", { name: resolving?.object_label ?? "" })}
  onclose={() => (resolving = null)}
>
  {#if resolving}
    <form class="stack" onsubmit={resolve}>
      <fieldset class="stack">
        <legend>{t("admin.reports.what")}</legend>
        {#each actionsFor(resolving) as a (a)}
          <label class="radio">
            <input type="radio" bind:group={action} value={a} />
            <span>
              {t(`admin.reports.action_${a}`)}
              <span class="muted block">{t(`admin.reports.action_${a}_hint`)}</span>
            </span>
          </label>
        {/each}
      </fieldset>
      <Field label={t("admin.reports.note")} hint={t("admin.reports.note_hint")} bind:value={note} required multiline rows={3} />
      <div class="row">
        <Button type="submit" variant={action === "takedown" ? "danger" : "primary"} {busy} disabled={!note.trim()}
          >{t("admin.reports.resolve")}</Button
        >
        <Button variant="secondary" onclick={() => (resolving = null)}>{t("common.cancel")}</Button>
      </div>
    </form>
  {/if}
</Dialog>

<style>
  .head {
    justify-content: space-between;
  }
  .head h2 {
    margin: 0;
  }
  .items {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .items li {
    padding: 0.85rem 0;
    gap: 0.4rem;
  }
  .items li + li {
    border-top: 1px solid var(--border);
  }
  .items p {
    margin: 0;
  }
  .line {
    gap: 0.6rem;
  }
  blockquote {
    margin: 0;
    padding: 0.5rem 0.85rem;
    border-inline-start: 3px solid var(--border);
    background: var(--surface-2);
    border-radius: var(--radius-sm);
  }
  blockquote p {
    margin: 0.25rem 0 0;
    white-space: pre-line;
  }
  .details {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  fieldset {
    border: 0;
    padding: 0;
    margin: 0;
    gap: 0.25rem;
  }
  legend {
    padding: 0;
    margin-bottom: 0.25rem;
    font-weight: 600;
  }
  .radio {
    display: flex;
    align-items: flex-start;
    gap: 0.5rem;
    min-height: 44px;
    padding-top: 0.5rem;
    cursor: pointer;
  }
  .block {
    display: block;
    font-size: 0.9rem;
  }
</style>
