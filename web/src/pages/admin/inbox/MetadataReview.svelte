<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Inbox tab: books whose online match was too uncertain to apply
  // (brief §6.12). Review opens the metadata editor; "Keep as is" clears it.
  import { api, type Book, type Schemas } from "../../../api/client";
  import Alert from "../../../components/Alert.svelte";
  import Button from "../../../components/Button.svelte";
  import Cover from "../../../components/Cover.svelte";
  import MetadataDialog from "../../../components/MetadataDialog.svelte";
  import { ago } from "../../../lib/format";
  import { t, tError } from "../../../lib/i18n";
  import { live } from "../../../lib/live.svelte";
  import { link } from "../../../lib/router.svelte";

  type Item = Schemas["MetadataReviewItem"];

  let { onchange }: { onchange: () => void } = $props();

  let items = $state<Item[]>([]);
  let next = $state<string | undefined>();
  let error = $state("");
  let busy = $state<string | null>(null);
  let editing = $state<Book | null>(null);

  async function load(cursor?: string) {
    const { data, error: err } = await api.GET("/admin/metadata-review", { params: { query: { cursor } } });
    if (err || !data) {
      error = tError(err?.code, err?.message);
      return;
    }
    items = cursor ? [...items, ...data.items] : data.items;
    next = data.next_cursor;
  }
  $effect(() => {
    void live.tick;
    void load();
  });

  async function review(it: Item) {
    busy = it.book.id;
    const { data, error: err } = await api.GET("/books/{bookId}", { params: { path: { bookId: it.book.id } } });
    busy = null;
    if (err || !data) error = tError(err?.code, err?.message);
    else editing = data;
  }

  async function dismiss(it: Item) {
    busy = it.book.id;
    const { error: err } = await api.POST("/admin/metadata-review/{bookId}/dismiss", { params: { path: { bookId: it.book.id } } });
    busy = null;
    if (err) error = tError(err.code, err.message);
    await load();
    onchange();
  }
</script>

<section class="card stack" aria-labelledby="mr-title">
  <h2 id="mr-title">{t("admin.meta.title")}</h2>
  <p class="muted">{t("admin.meta.intro")}</p>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  {#if items.length}
    <ul class="items">
      {#each items as it (it.book.id)}
        <li>
          <div class="cover"><Cover src="/api/v1/books/{it.book.id}/cover" has={it.book.has_cover} title={it.book.title} size={128} /></div>
          <div class="stack body">
            <a href="/books/{it.book.id}" use:link><strong>{it.book.title}</strong></a>
            <p class="muted">
              {[it.authors.join(", ") || t("admin.meta.no_author"), ago(it.created_at)].join(" · ")}
            </p>
            <p class="muted">
              {it.score != null && it.score > 0 ? t("admin.meta.best", { pct: Math.round(it.score * 100) }) : t("admin.meta.none")}
            </p>
            <div class="row">
              <Button busy={busy === it.book.id} onclick={() => review(it)}>{t("admin.meta.review")}</Button>
              <Button variant="secondary" onclick={() => dismiss(it)}>{t("admin.meta.keep")}</Button>
            </div>
          </div>
        </li>
      {/each}
    </ul>
    {#if next}<div><Button variant="secondary" onclick={() => load(next)}>{t("reviews.more")}</Button></div>{/if}
  {:else}
    <p class="muted">{t("admin.inbox.empty")}</p>
  {/if}
</section>

{#if editing}
  <MetadataDialog
    open={!!editing}
    book={editing}
    onclose={() => (editing = null)}
    onsaved={async () => {
      await load();
      onchange();
    }}
  />
{/if}

<style>
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
