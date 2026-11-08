<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Admin Trash (brief §6.5 rule 7): restore, purge one, empty now (type the
  // count + password + 2FA code). Books are purged automatically after 30 days.
  import { api, type Schemas } from "../../api/client";
  import AdminLayout from "../../components/AdminLayout.svelte";
  import Alert from "../../components/Alert.svelte";
  import Button from "../../components/Button.svelte";
  import Dialog from "../../components/Dialog.svelte";
  import Field from "../../components/Field.svelte";
  import { bytes, dateTime } from "../../lib/format";
  import { t, tError } from "../../lib/i18n";
  import { live } from "../../lib/live.svelte";

  type Book = Schemas["MasterBook"];
  let books = $state<Book[]>([]);
  let error = $state("");
  let notice = $state("");

  async function load() {
    const { data, error: err } = await api.GET("/admin/books", { params: { query: { status: "trashed", limit: 200 } } });
    if (err || !data) error = tError(err?.code, err?.message);
    else books = data.items;
  }
  $effect(() => {
    void live.tick;
    void load();
  });

  async function restore(b: Book) {
    const { error: err } = await api.POST("/admin/books/{bookId}/restore", { params: { path: { bookId: b.id } } });
    if (err) error = tError(err.code, err.message);
    else notice = t("admin.trash.restored", { title: b.title });
    await load();
  }

  let purging = $state<Book | null>(null);
  async function purge() {
    if (!purging) return;
    const b = purging;
    const { error: err } = await api.POST("/admin/books/{bookId}/purge", { params: { path: { bookId: b.id } } });
    purging = null;
    if (err) error = tError(err.code, err.message);
    else notice = t("admin.trash.purged", { title: b.title });
    await load();
  }

  let emptyOpen = $state(false);
  let count = $state("");
  let password = $state("");
  let code = $state("");
  let emptyError = $state("");
  let busy = $state(false);
  async function empty(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    emptyError = "";
    const { error: err } = await api.POST("/admin/trash/empty", {
      body: { confirm_count: Number(count), password, totp_code: code.trim() },
    });
    busy = false;
    if (err) {
      emptyError = tError(err.code, err.message);
      return;
    }
    emptyOpen = false;
    count = password = code = "";
    notice = t("admin.trash.emptied");
    await load();
  }
</script>

<AdminLayout title={t("admin.nav.trash")}>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  {#if notice}<Alert kind="ok">{notice}</Alert>{/if}
  <p class="muted">{t("admin.trash.help")}</p>
  {#if books.length}
    <div class="row">
      <Button variant="danger" onclick={() => (emptyOpen = true)}>{t("admin.trash.empty", { n: books.length })}</Button>
    </div>
    <div class="table-scroll">
      <table class="data">
        <thead>
          <tr>
            <th scope="col">{t("admin.col.title")}</th>
            <th scope="col" class="num">{t("admin.col.size")}</th>
            <th scope="col">{t("admin.trash.trashed_at")}</th>
            <th scope="col">{t("admin.book.purge_at")}</th>
            <th scope="col"><span class="sr-only">{t("admin.col.actions")}</span></th>
          </tr>
        </thead>
        <tbody>
          {#each books as b (b.id)}
            <tr>
              <td>{b.title}</td>
              <td class="num">{bytes(b.size_bytes)}</td>
              <td>{b.trashed_at ? dateTime(b.trashed_at) : "—"}</td>
              <td>{b.purge_at ? dateTime(b.purge_at) : "—"}</td>
              <td class="actions">
                <Button variant="secondary" onclick={() => restore(b)}>{t("admin.trash.restore")}</Button>
                <Button variant="ghost" onclick={() => (purging = b)}>{t("admin.trash.purge")}</Button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <p class="muted">{t("admin.trash.none")}</p>
  {/if}
</AdminLayout>

<Dialog open={!!purging} title={t("admin.trash.purge_title", { title: purging?.title ?? "" })} onclose={() => (purging = null)}>
  <p>{t("admin.trash.purge_body")}</p>
  <div class="row">
    <Button variant="danger" onclick={purge}>{t("admin.trash.purge")}</Button>
    <Button variant="secondary" onclick={() => (purging = null)}>{t("common.cancel")}</Button>
  </div>
</Dialog>

<Dialog open={emptyOpen} title={t("admin.trash.empty", { n: books.length })} onclose={() => (emptyOpen = false)}>
  <form class="stack" onsubmit={empty}>
    <p>{t("admin.trash.empty_body", { n: books.length })}</p>
    <Field label={t("admin.trash.type_count", { n: books.length })} bind:value={count} inputmode="numeric" required />
    <Field label={t("security.current_password")} type="password" bind:value={password} autocomplete="current-password" required />
    <Field label={t("auth.totp_code")} bind:value={code} inputmode="numeric" autocomplete="one-time-code" required />
    {#if emptyError}<Alert kind="error">{emptyError}</Alert>{/if}
    <div class="row">
      <Button variant="danger" type="submit" {busy} disabled={count !== String(books.length)}>{t("admin.trash.empty_confirm")}</Button>
      <Button variant="secondary" onclick={() => (emptyOpen = false)}>{t("common.cancel")}</Button>
    </div>
  </form>
</Dialog>

<style>
  .actions {
    white-space: nowrap;
    text-align: end;
  }
</style>
