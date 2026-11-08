<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // One master-store book: who it's shared with, takedown, Trash.
  import { api, type Schemas } from "../../api/client";
  import AdminLayout from "../../components/AdminLayout.svelte";
  import Alert from "../../components/Alert.svelte";
  import Button from "../../components/Button.svelte";
  import Dialog from "../../components/Dialog.svelte";
  import DistributeDialog from "../../components/DistributeDialog.svelte";
  import Field from "../../components/Field.svelte";
  import { bytes, dateTime, duration } from "../../lib/format";
  import { t, tError } from "../../lib/i18n";
  import { link } from "../../lib/router.svelte";

  let { id }: { id: string } = $props();

  let book = $state<Schemas["MasterBook"] | null>(null);
  let error = $state("");
  let notice = $state("");

  async function load() {
    const { data, error: err } = await api.GET("/admin/books/{bookId}", { params: { path: { bookId: id } } });
    if (err || !data) error = tError(err?.code, err?.message);
    else book = data;
  }
  $effect(() => {
    void load();
  });

  async function revoke(distId: string) {
    const { error: err } = await api.DELETE("/admin/distributions/{distributionId}", { params: { path: { distributionId: distId } } });
    if (err) error = tError(err.code, err.message);
    await load();
  }

  let distOpen = $state(false);

  let takedownOpen = $state(false);
  let reason = $state("");
  async function takedown(e: SubmitEvent) {
    e.preventDefault();
    const { error: err } = await api.POST("/admin/books/{bookId}/takedown", {
      params: { path: { bookId: id } },
      body: { reason: reason.trim() },
    });
    takedownOpen = false;
    reason = "";
    if (err) error = tError(err.code, err.message);
    else notice = t("admin.book.taken_down_notice");
    await load();
  }
  async function lift() {
    const { error: err } = await api.DELETE("/admin/books/{bookId}/takedown", { params: { path: { bookId: id } } });
    if (err) error = tError(err.code, err.message);
    else notice = t("admin.book.lifted_notice");
    await load();
  }

  let trashOpen = $state(false);
  async function trash() {
    const { error: err } = await api.DELETE("/admin/books/{bookId}", { params: { path: { bookId: id } } });
    trashOpen = false;
    if (err) error = tError(err.code, err.message);
    else notice = t("admin.book.trashed_notice");
    await load();
  }
  async function restore() {
    const { error: err } = await api.POST("/admin/books/{bookId}/restore", { params: { path: { bookId: id } } });
    if (err) error = tError(err.code, err.message);
    else notice = t("admin.book.restored_notice");
    await load();
  }
</script>

<AdminLayout title={book?.title ?? t("common.loading")}>
  <p><a href="/admin/library" use:link>← {t("admin.nav.library")}</a></p>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  {#if notice}<Alert kind="ok">{notice}</Alert>{/if}
  {#if book}
    {#if book.takedown}
      <Alert kind="error"
        >{t("admin.book.taken_down_banner", { reason: book.takedown.reason, date: dateTime(book.takedown.taken_at) })}</Alert
      >
    {/if}
    <section class="card stack" aria-labelledby="b-info">
      <h2 id="b-info">{t("admin.book.details")}</h2>
      <dl class="facts">
        <div>
          <dt>{t("book.by")}</dt>
          <dd>{book.authors.map((a) => a.name).join(", ") || "—"}</dd>
        </div>
        <div>
          <dt>{t("admin.col.status")}</dt>
          <dd>
            <span class="pill" class:warn={book.status !== "ready"}>{t(`admin.book_status.${book.status}`)}</span>
            {#if book.status_reason}<span class="muted"> {book.status_reason}</span>{/if}
          </dd>
        </div>
        <div>
          <dt>{t("admin.col.length")}</dt>
          <dd>{duration(book.duration_ms)}</dd>
        </div>
        <div>
          <dt>{t("admin.col.size")}</dt>
          <dd>{bytes(book.size_bytes)}</dd>
        </div>
        <div>
          <dt>{t("admin.book.origin")}</dt>
          <dd>{t(`admin.origin.${book.origin}`)}</dd>
        </div>
        <div>
          <dt>{t("admin.book.folder")}</dt>
          <dd class="mono">{book.folder_path}</dd>
        </div>
        <div>
          <dt>{t("admin.col.created")}</dt>
          <dd>{dateTime(book.created_at)}</dd>
        </div>
        {#if book.uploader_id}
          <div>
            <dt>{t("admin.book.uploader")}</dt>
            <dd><a href="/admin/users/{book.uploader_id}" use:link>{t("admin.book.uploader_link")}</a></dd>
          </div>
        {/if}
        {#if book.purge_at}<div>
            <dt>{t("admin.book.purge_at")}</dt>
            <dd>{dateTime(book.purge_at)}</dd>
          </div>{/if}
      </dl>
    </section>

    {#if book.status !== "trashed"}
      <section class="card stack" aria-labelledby="b-dist">
        <h2 id="b-dist">{t("admin.book.shared_with")}</h2>
        {#if book.distributions.length}
          <ul class="dists">
            {#each book.distributions as d (d.id)}
              <li>
                <span
                  ><span class="pill">{t(`admin.dist.${d.target_type}`)}</span>
                  {d.target_name} <span class="muted">· {dateTime(d.created_at)}</span></span
                >
                <Button variant="ghost" onclick={() => revoke(d.id)}>{t("admin.book.unshare")}</Button>
              </li>
            {/each}
          </ul>
        {:else}
          <p class="muted">{t("admin.book.not_shared")}</p>
        {/if}
        <div class="row"><Button onclick={() => (distOpen = true)}>{t("admin.lib.distribute")}</Button></div>
      </section>
    {/if}

    <section class="card stack" aria-labelledby="b-actions">
      <h2 id="b-actions">{t("admin.book.actions")}</h2>
      <div class="row">
        {#if book.status === "trashed"}
          <Button onclick={restore}>{t("admin.trash.restore")}</Button>
        {:else}
          {#if book.takedown}
            <Button variant="secondary" onclick={lift}>{t("admin.book.lift")}</Button>
          {:else}
            <Button variant="secondary" onclick={() => (takedownOpen = true)}>{t("admin.book.takedown")}</Button>
          {/if}
          <Button variant="danger" onclick={() => (trashOpen = true)}>{t("admin.lib.trash")}</Button>
          {#if book.status === "ready"}<a class="btnlink" href="/books/{book.id}" use:link>{t("admin.book.open")}</a>{/if}
        {/if}
      </div>
    </section>
  {/if}
</AdminLayout>

<DistributeDialog bookIds={[id]} open={distOpen} onclose={() => (distOpen = false)} ondone={load} />

<Dialog open={takedownOpen} title={t("admin.book.takedown")} onclose={() => (takedownOpen = false)}>
  <form class="stack" onsubmit={takedown}>
    <p class="muted">{t("admin.book.takedown_help")}</p>
    <Field label={t("admin.reason")} bind:value={reason} required multiline rows={3} />
    <div class="row">
      <Button variant="danger" type="submit" disabled={!reason.trim()}>{t("admin.book.takedown")}</Button>
      <Button variant="secondary" onclick={() => (takedownOpen = false)}>{t("common.cancel")}</Button>
    </div>
  </form>
</Dialog>

<Dialog open={trashOpen} title={t("admin.lib.trash_title", { n: 1 })} onclose={() => (trashOpen = false)}>
  <p>{t("admin.lib.trash_body")}</p>
  <div class="row">
    <Button variant="danger" onclick={trash}>{t("admin.lib.trash")}</Button>
    <Button variant="secondary" onclick={() => (trashOpen = false)}>{t("common.cancel")}</Button>
  </div>
</Dialog>

<style>
  .facts {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr));
    gap: 0.75rem 1.5rem;
    margin: 0;
  }
  .facts dt {
    color: var(--text-muted);
    font-size: 0.875rem;
  }
  .facts dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  .dists {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .dists li {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 1rem;
    padding: 0.35rem 0;
  }
  .dists li + li {
    border-top: 1px solid var(--border);
  }
  .btnlink {
    min-height: 48px;
    display: inline-flex;
    align-items: center;
    padding: 0 1.25rem;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    color: var(--text);
    font-weight: 600;
    text-decoration: none;
  }
</style>
