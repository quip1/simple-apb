<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Master store browser (brief §6.23) with distribution and bulk actions.
  import { api, type Schemas } from "../../api/client";
  import AdminLayout from "../../components/AdminLayout.svelte";
  import Alert from "../../components/Alert.svelte";
  import Button from "../../components/Button.svelte";
  import Dialog from "../../components/Dialog.svelte";
  import DistributeDialog from "../../components/DistributeDialog.svelte";
  import { bytes, duration } from "../../lib/format";
  import { t, tError } from "../../lib/i18n";
  import { live } from "../../lib/live.svelte";
  import { link } from "../../lib/router.svelte";

  type Book = Schemas["MasterBook"];
  const STATUSES = ["", "ready", "processing", "quarantined"] as const;

  let status = $state<(typeof STATUSES)[number]>("");
  let q = $state("");
  let query = $state("");
  let books = $state<Book[]>([]);
  let next = $state<string | undefined>();
  let error = $state("");
  let notice = $state("");
  let selected = $state<string[]>([]);

  let timer: ReturnType<typeof setTimeout> | undefined;
  function onSearch() {
    clearTimeout(timer);
    timer = setTimeout(() => (query = q.trim()), 250);
  }

  async function load(append = false) {
    const { data, error: err } = await api.GET("/admin/books", {
      params: { query: { status: status || undefined, q: query || undefined, cursor: append ? next : undefined, limit: 50 } },
    });
    if (err || !data) {
      error = tError(err?.code, err?.message);
      return;
    }
    books = append ? [...books, ...data.items] : data.items;
    next = data.next_cursor;
  }
  $effect(() => {
    void [status, query, live.tick];
    selected = [];
    void load();
  });

  const allSelected = $derived(books.length > 0 && selected.length === books.length);
  function toggleAll(on: boolean) {
    selected = on ? books.map((b) => b.id) : [];
  }
  function toggle(id: string, on: boolean) {
    selected = on ? [...selected, id] : selected.filter((x) => x !== id);
  }

  let distOpen = $state(false);
  let trashOpen = $state(false);
  let busy = $state(false);
  async function trashSelected() {
    busy = true;
    for (const id of selected) await api.DELETE("/admin/books/{bookId}", { params: { path: { bookId: id } } });
    busy = false;
    trashOpen = false;
    notice = t("admin.lib.trashed", { n: selected.length });
    await load();
  }

  let scanning = $state(false);
  async function scan() {
    scanning = true;
    const { data, error: err } = await api.POST("/admin/library/scan");
    if (err || !data) {
      scanning = false;
      error = tError(err?.code, err?.message);
      return;
    }
    // Wait for the scan job (up to a minute), then refresh.
    for (let i = 0; i < 60; i++) {
      await new Promise((r) => setTimeout(r, 1000));
      const j = await api.GET("/jobs/{jobId}", { params: { path: { jobId: data.job_id } } });
      if (j.data && (j.data.status === "succeeded" || j.data.status === "failed")) {
        const r = (j.data.result ?? {}) as Record<string, number>;
        notice =
          j.data.status === "succeeded"
            ? t("admin.lib.scan_done", { added: r.added ?? 0, updated: r.updated ?? 0, missing: r.missing ?? 0 })
            : t("admin.lib.scan_failed");
        break;
      }
    }
    scanning = false;
    await load();
  }
</script>

<AdminLayout title={t("admin.nav.library")}>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  {#if notice}<Alert kind="ok">{notice}</Alert>{/if}
  <div class="row">
    <label class="grow">
      <span class="sr-only">{t("admin.lib.search")}</span>
      <input class="text" type="search" bind:value={q} oninput={onSearch} placeholder={t("admin.lib.search")} />
    </label>
    <label>
      <span class="sr-only">{t("admin.filter_status")}</span>
      <select bind:value={status}>
        {#each STATUSES as s (s)}<option value={s}>{s ? t(`admin.book_status.${s}`) : t("admin.lib.all_statuses")}</option>{/each}
      </select>
    </label>
    <Button variant="secondary" busy={scanning} onclick={scan}>{t("admin.lib.scan")}</Button>
  </div>

  {#if selected.length}
    <div class="row bulk" role="region" aria-label={t("admin.lib.bulk")}>
      <span>{t("admin.lib.selected", { n: selected.length })}</span>
      <Button onclick={() => (distOpen = true)}>{t("admin.lib.distribute")}</Button>
      <Button variant="danger" onclick={() => (trashOpen = true)}>{t("admin.lib.trash")}</Button>
      <Button variant="ghost" onclick={() => (selected = [])}>{t("admin.lib.clear_selection")}</Button>
    </div>
  {/if}

  <div class="table-scroll">
    <table class="data">
      <thead>
        <tr>
          <th scope="col">
            <input
              type="checkbox"
              checked={allSelected}
              onchange={(e) => toggleAll(e.currentTarget.checked)}
              aria-label={t("admin.lib.select_all")}
            />
          </th>
          <th scope="col">{t("admin.col.title")}</th>
          <th scope="col">{t("admin.col.status")}</th>
          <th scope="col">{t("admin.col.shared_with")}</th>
          <th scope="col" class="num">{t("admin.col.length")}</th>
          <th scope="col" class="num">{t("admin.col.size")}</th>
        </tr>
      </thead>
      <tbody>
        {#each books as b (b.id)}
          <tr>
            <td>
              <input
                type="checkbox"
                checked={selected.includes(b.id)}
                onchange={(e) => toggle(b.id, e.currentTarget.checked)}
                aria-label={t("admin.lib.select", { title: b.title })}
              />
            </td>
            <td>
              <a href="/admin/books/{b.id}" use:link>{b.title}</a>
              {#if b.authors.length}<div class="muted small">{b.authors.map((a) => a.name).join(", ")}</div>{/if}
            </td>
            <td>
              <span class="pill" class:warn={b.status !== "ready"}>{t(`admin.book_status.${b.status}`)}</span>
              {#if b.takedown}<span class="pill warn">{t("admin.lib.taken_down")}</span>{/if}
            </td>
            <td
              >{b.distributions.length
                ? b.distributions.map((d) => d.target_name).join(", ")
                : b.uploader_id
                  ? t("admin.lib.uploader_only")
                  : "—"}</td
            >
            <td class="num">{duration(b.duration_ms)}</td>
            <td class="num">{bytes(b.size_bytes)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  {#if !books.length}<p class="muted">{t("admin.lib.none")}</p>{/if}
  {#if next}<div class="row"><Button variant="secondary" onclick={() => load(true)}>{t("library.load_more")}</Button></div>{/if}
</AdminLayout>

<DistributeDialog bookIds={selected} open={distOpen} onclose={() => (distOpen = false)} ondone={() => load()} />

<Dialog open={trashOpen} title={t("admin.lib.trash_title", { n: selected.length })} onclose={() => (trashOpen = false)}>
  <p>{t("admin.lib.trash_body")}</p>
  <div class="row">
    <Button variant="danger" {busy} onclick={trashSelected}>{t("admin.lib.trash")}</Button>
    <Button variant="secondary" onclick={() => (trashOpen = false)}>{t("common.cancel")}</Button>
  </div>
</Dialog>

<style>
  .grow {
    flex: 1 1 16rem;
  }
  .bulk {
    padding: 0.5rem 0.75rem;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface);
  }
  .small {
    font-size: 0.85rem;
  }
  input[type="checkbox"] {
    width: 1.2rem;
    height: 1.2rem;
    accent-color: var(--accent);
  }
</style>
