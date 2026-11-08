<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // A book's bookmarks: jump to, add with a note, edit the note, delete.
  // Bookmarks sync last-write-wins with client ids (brief §6.8).
  import { api, type Schemas } from "../api/client";
  import { clock } from "../lib/format";
  import { t, tError } from "../lib/i18n";
  import { live } from "../lib/live.svelte";
  import Alert from "./Alert.svelte";
  import Button from "./Button.svelte";

  type Bookmark = Schemas["Bookmark"];

  let {
    bookId,
    onjump,
    addAt,
  }: {
    bookId: string;
    onjump: (ms: number) => void;
    /** Current position, when adding is possible (the player). */
    addAt?: () => number;
  } = $props();

  let items = $state<Bookmark[]>([]);
  let error = $state("");
  let note = $state("");
  let adding = $state(false);
  let editing = $state<string | null>(null);
  let editNote = $state("");

  async function load(id: string) {
    const { data, error: err } = await api.GET("/me/bookmarks", { params: { query: { book_id: id } } });
    if (err) error = tError(err.code, err.message);
    else items = data ?? [];
  }

  $effect(() => {
    void live.tick;
    void load(bookId);
  });

  async function add(e: SubmitEvent) {
    e.preventDefault();
    if (!addAt) return;
    adding = true;
    const { error: err } = await api.POST("/me/bookmarks", {
      body: {
        id: crypto.randomUUID(),
        book_id: bookId,
        position_ms: Math.round(addAt()),
        note: note.trim() || undefined,
        client_ts: new Date().toISOString(),
      },
    });
    adding = false;
    if (err) error = tError(err.code, err.message);
    else {
      note = "";
      await load(bookId);
    }
  }

  async function saveNote(b: Bookmark) {
    await api.PATCH("/me/bookmarks/{bookmarkId}", {
      params: { path: { bookmarkId: b.id } },
      body: { note: editNote.trim(), client_ts: new Date().toISOString() },
    });
    editing = null;
    await load(bookId);
  }

  async function remove(b: Bookmark) {
    await api.DELETE("/me/bookmarks/{bookmarkId}", { params: { path: { bookmarkId: b.id } } });
    await load(bookId);
  }
</script>

<div class="stack">
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  {#if addAt}
    <form class="stack" onsubmit={add}>
      <label class="stack">
        <span>{t("bookmarks.note_label")}</span>
        <textarea class="text" bind:value={note} maxlength="2000" rows="2" placeholder={t("bookmarks.note_placeholder")}></textarea>
      </label>
      <Button type="submit" busy={adding}>{t("bookmarks.add_here", { time: clock(addAt()) })}</Button>
    </form>
  {/if}
  {#if items.length}
    <ul>
      {#each items as b (b.id)}
        <li class="stack">
          <div class="row">
            <Button variant="secondary" onclick={() => onjump(b.position_ms)}>
              {t("bookmarks.jump", { time: clock(b.position_ms) })}
            </Button>
            <Button
              variant="ghost"
              onclick={() => {
                editing = b.id;
                editNote = b.note ?? "";
              }}>{t("bookmarks.edit")}</Button
            >
            <Button variant="ghost" onclick={() => remove(b)}>{t("bookmarks.delete")}</Button>
          </div>
          {#if editing === b.id}
            <textarea class="text" bind:value={editNote} maxlength="2000" rows="2" aria-label={t("bookmarks.note_label")}></textarea>
            <div class="row">
              <Button onclick={() => saveNote(b)}>{t("common.save")}</Button>
              <Button variant="secondary" onclick={() => (editing = null)}>{t("common.cancel")}</Button>
            </div>
          {:else if b.note}
            <p class="note">{b.note}</p>
          {/if}
        </li>
      {/each}
    </ul>
  {:else}
    <p class="muted">{t("bookmarks.none")}</p>
  {/if}
</div>

<style>
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  li {
    padding: 0.75rem 0;
  }
  li + li {
    border-top: 1px solid var(--border);
  }
  .note {
    margin: 0;
    white-space: pre-wrap;
  }
</style>
