<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Inbox tab: requests to share books with Public (brief §6.5 rule 5).
  import { api, type Schemas } from "../../../api/client";
  import Alert from "../../../components/Alert.svelte";
  import Button from "../../../components/Button.svelte";
  import Cover from "../../../components/Cover.svelte";
  import Dialog from "../../../components/Dialog.svelte";
  import Field from "../../../components/Field.svelte";
  import { ago } from "../../../lib/format";
  import { t, tError } from "../../../lib/i18n";
  import { live } from "../../../lib/live.svelte";
  import { link } from "../../../lib/router.svelte";

  type Item = Schemas["ReviewQueueItem"];
  type Status = Item["status"];

  let { onchange }: { onchange: () => void } = $props();

  let status = $state<Status>("pending");
  let items = $state<Item[]>([]);
  let next = $state<string | undefined>();
  let error = $state("");
  let busy = $state<string | null>(null);

  async function load(s: Status, cursor?: string) {
    const { data, error: err } = await api.GET("/admin/review-queue", { params: { query: { status: s, cursor } } });
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

  async function approve(it: Item) {
    busy = it.id;
    error = "";
    const { error: err } = await api.POST("/admin/review-queue/{itemId}/approve", { params: { path: { itemId: it.id } } });
    busy = null;
    if (err) error = tError(err.code, err.message);
    await load(status);
    onchange();
  }

  let rejecting = $state<Item | null>(null);
  let reason = $state("");
  async function reject(e: SubmitEvent) {
    e.preventDefault();
    if (!rejecting || !reason.trim()) return;
    const it = rejecting;
    busy = it.id;
    const { error: err } = await api.POST("/admin/review-queue/{itemId}/reject", {
      params: { path: { itemId: it.id } },
      body: { reason: reason.trim() },
    });
    busy = null;
    rejecting = null;
    reason = "";
    if (err) error = tError(err.code, err.message);
    await load(status);
    onchange();
  }
</script>

<section class="card stack" aria-labelledby="rq-title">
  <div class="row head">
    <h2 id="rq-title">{t("admin.queue.title")}</h2>
    <label>
      <span class="sr-only">{t("admin.filter_status")}</span>
      <select bind:value={status}>
        {#each ["pending", "approved", "rejected"] as s (s)}<option value={s}>{t(`admin.queue.${s}`)}</option>{/each}
      </select>
    </label>
  </div>
  <p class="muted">{t("admin.queue.intro")}</p>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  {#if items.length}
    <ul class="items">
      {#each items as it (it.id)}
        <li>
          <div class="cover"><Cover src="/api/v1/books/{it.book.id}/cover" has={it.book.has_cover} title={it.book.title} size={128} /></div>
          <div class="stack body">
            <a href="/admin/books/{it.book.id}" use:link><strong>{it.book.title}</strong></a>
            <p class="muted">
              {t("admin.queue.line", {
                user: it.uploader?.display_name ?? t("reviews.former_user"),
                group: it.target.name,
                when: ago(it.created_at),
              })}
            </p>
            {#if it.status === "pending"}
              <div class="row">
                <Button busy={busy === it.id} onclick={() => approve(it)}>{t("admin.queue.approve")}</Button>
                <Button variant="secondary" onclick={() => (rejecting = it)}>{t("admin.queue.reject")}</Button>
                <a href="/books/{it.book.id}" use:link>{t("admin.queue.listen")}</a>
              </div>
            {:else}
              <p>
                <span class="pill">{t(`admin.queue.${it.status}`)}</span>
                {#if it.decided_by}<span class="muted">{t("admin.queue.by", { name: it.decided_by.display_name })}</span>{/if}
              </p>
              {#if it.reason}<p class="muted">{it.reason}</p>{/if}
            {/if}
          </div>
        </li>
      {/each}
    </ul>
    {#if next}<div><Button variant="secondary" onclick={() => load(status, next)}>{t("reviews.more")}</Button></div>{/if}
  {:else}
    <p class="muted">{t("admin.inbox.empty")}</p>
  {/if}
</section>

<Dialog open={!!rejecting} title={t("admin.queue.reject_title", { title: rejecting?.book.title ?? "" })} onclose={() => (rejecting = null)}>
  <form class="stack" onsubmit={reject}>
    <Field label={t("admin.queue.reject_reason")} bind:value={reason} required multiline rows={3} />
    <div class="row">
      <Button variant="danger" type="submit" disabled={!reason.trim()}>{t("admin.queue.reject")}</Button>
      <Button variant="secondary" onclick={() => (rejecting = null)}>{t("common.cancel")}</Button>
    </div>
  </form>
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
    display: flex;
    gap: 1rem;
    padding: 0.85rem 0;
  }
  .items li + li {
    border-top: 1px solid var(--border);
  }
  .cover {
    width: 4rem;
    flex: none;
  }
  .body {
    gap: 0.35rem;
    min-width: 0;
  }
  .body p {
    margin: 0;
  }
</style>
