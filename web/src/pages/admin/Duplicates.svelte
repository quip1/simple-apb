<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Duplicate finder (brief §6.13): pairs of books that look alike, side by
  // side. "Keep both" closes the pair; "Keep this one" moves the other book's
  // listeners, progress, bookmarks and reviews to it and puts it in the Trash.
  import { api, type Schemas } from "../../api/client";
  import AdminLayout from "../../components/AdminLayout.svelte";
  import Alert from "../../components/Alert.svelte";
  import Button from "../../components/Button.svelte";
  import { bytes, dateTime, duration } from "../../lib/format";
  import { t, tError } from "../../lib/i18n";
  import { waitForJob } from "../../lib/jobs";
  import { link } from "../../lib/router.svelte";

  type Pair = Schemas["DuplicateCandidate"];
  type Book = Schemas["MasterBook"];

  let pairs = $state<Pair[] | null>(null);
  let error = $state("");
  let notice = $state("");
  let scanning = $state(false);
  let busy = $state<string | null>(null);

  async function load() {
    const { data, error: err } = await api.GET("/admin/duplicates", { params: { query: { limit: 50 } } });
    if (err || !data) error = tError(err?.code, err?.message);
    else pairs = data.items;
  }
  $effect(() => {
    void load();
  });

  async function scan() {
    error = notice = "";
    scanning = true;
    const { data, error: err } = await api.POST("/admin/duplicates/scan");
    if (!data) {
      scanning = false;
      error = tError(err?.code, err?.message);
      return;
    }
    const job = await waitForJob(data.job_id);
    scanning = false;
    const found = Number(job?.result?.new_pairs ?? 0);
    notice = job?.status === "succeeded" ? t("admin.duplicates.scanned", { n: found }) : t("admin.duplicates.scan_failed");
    await load();
  }

  async function resolve(p: Pair, keep: Book | null) {
    error = notice = "";
    busy = p.id;
    const { error: err } = await api.POST("/admin/duplicates/{duplicateId}", {
      params: { path: { duplicateId: p.id } },
      body: keep ? { action: "merge", keep_book_id: keep.id } : { action: "keep" },
    });
    busy = null;
    if (err) error = tError(err.code, err.message);
    else notice = keep ? t("admin.duplicates.merged", { title: keep.title }) : t("admin.duplicates.kept");
    await load();
  }
</script>

{#snippet side(b: Book, p: Pair)}
  <div class="side stack-tight">
    <a class="title" href="/admin/books/{b.id}" use:link>{b.title}</a>
    <span>{b.authors.map((a) => a.name).join(", ") || t("admin.duplicates.no_author")}</span>
    <span class="muted">{duration(b.duration_ms)} · {bytes(b.size_bytes)}</span>
    <span class="muted path">{b.folder_path}</span>
    <span class="muted">{t("admin.duplicates.added", { date: dateTime(b.created_at) })}</span>
    <span class="muted">{t("admin.duplicates.shared", { n: b.distributions.length })}</span>
    <div>
      <Button variant="secondary" busy={busy === p.id} onclick={() => void resolve(p, b)}>{t("admin.duplicates.keep_this")}</Button>
    </div>
  </div>
{/snippet}

<AdminLayout title={t("admin.nav.duplicates")}>
  <p class="muted">{t("admin.duplicates.help")}</p>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  {#if notice}<Alert kind="ok">{notice}</Alert>{/if}
  <div><Button variant="secondary" busy={scanning} onclick={() => void scan()}>{t("admin.duplicates.scan")}</Button></div>
  {#if pairs === null}
    <p class="muted" role="status">{t("common.loading")}</p>
  {:else if !pairs.length}
    <p>{t("admin.duplicates.none")}</p>
  {:else}
    <ul class="pairs stack">
      {#each pairs as p (p.id)}
        <li class="card stack">
          <p class="kind">
            {p.kind === "exact" ? t("admin.duplicates.exact") : t("admin.duplicates.probable", { pct: Math.round((p.score ?? 0) * 100) })}
          </p>
          <div class="sides">
            {@render side(p.book_a, p)}
            {@render side(p.book_b, p)}
          </div>
          <div>
            <Button variant="ghost" busy={busy === p.id} onclick={() => void resolve(p, null)}>{t("admin.duplicates.keep_both")}</Button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</AdminLayout>

<style>
  .pairs {
    list-style: none;
    padding: 0;
    margin: 0;
  }
  .sides {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(16rem, 1fr));
    gap: 1.25rem;
  }
  .stack-tight {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .title {
    font-weight: 600;
  }
  .kind {
    font-weight: 600;
    margin: 0;
  }
  .path {
    overflow-wrap: anywhere;
    font-family: ui-monospace, monospace;
    font-size: 0.85em;
  }
</style>
