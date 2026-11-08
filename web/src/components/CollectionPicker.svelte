<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // "Add to collection": tick the collections a book belongs to, or make one.
  import { api, type Schemas } from "../api/client";
  import { t, tError } from "../lib/i18n";
  import Alert from "./Alert.svelte";
  import Button from "./Button.svelte";
  import Dialog from "./Dialog.svelte";

  let { bookId, open, onclose }: { bookId: string; open: boolean; onclose: () => void } = $props();

  let collections = $state<Schemas["Collection"][]>([]);
  let error = $state("");
  let name = $state("");
  let busy = $state(false);

  async function load() {
    const { data, error: err } = await api.GET("/me/collections");
    if (err) error = tError(err.code, err.message);
    else collections = data ?? [];
  }

  $effect(() => {
    if (open) void load();
  });

  async function toggle(c: Schemas["Collection"], on: boolean) {
    const params = { params: { path: { collectionId: c.id, bookId } } };
    const { error: err } = on
      ? await api.PUT("/me/collections/{collectionId}/books/{bookId}", params)
      : await api.DELETE("/me/collections/{collectionId}/books/{bookId}", params);
    if (err) error = tError(err.code, err.message);
    await load();
  }

  async function create(e: SubmitEvent) {
    e.preventDefault();
    if (!name.trim()) return;
    busy = true;
    const id = crypto.randomUUID();
    const { error: err } = await api.POST("/me/collections", {
      body: { id, name: name.trim(), client_ts: new Date().toISOString() },
    });
    if (!err) await api.PUT("/me/collections/{collectionId}/books/{bookId}", { params: { path: { collectionId: id, bookId } } });
    busy = false;
    if (err) error = tError(err.code, err.message);
    else name = "";
    await load();
  }
</script>

<Dialog {open} title={t("collections.add_title")} {onclose}>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  {#if collections.length}
    <ul>
      {#each collections as c (c.id)}
        <li>
          <label class="check">
            <input type="checkbox" checked={c.book_ids.includes(bookId)} onchange={(e) => toggle(c, e.currentTarget.checked)} />
            <span>{c.name}</span>
          </label>
        </li>
      {/each}
    </ul>
  {/if}
  <form class="row" onsubmit={create}>
    <label class="grow">
      <span class="sr-only">{t("library.collection_name")}</span>
      <input class="text" bind:value={name} maxlength="100" placeholder={t("library.collection_new_placeholder")} />
    </label>
    <Button type="submit" {busy} disabled={!name.trim()}>{t("library.collection_create")}</Button>
  </form>
  <Button variant="secondary" onclick={onclose}>{t("common.done")}</Button>
</Dialog>

<style>
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .check {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    min-height: 48px;
    cursor: pointer;
  }
  .check input {
    width: 1.4rem;
    height: 1.4rem;
    accent-color: var(--accent);
  }
  .grow {
    flex: 1 1 12rem;
  }
</style>
