<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Recommend a book to people in your groups. Only people who can already
  // see the book get it; the rest are counted, never named.
  import { api, type Schemas } from "../api/client";
  import { t, tError } from "../lib/i18n";
  import Alert from "./Alert.svelte";
  import Button from "./Button.svelte";
  import Dialog from "./Dialog.svelte";
  import Field from "./Field.svelte";
  import PeoplePicker from "./PeoplePicker.svelte";

  let { open, bookId, title, onclose }: { open: boolean; bookId: string; title: string; onclose: () => void } = $props();

  let people = $state<Schemas["UserRef"][]>([]);
  let note = $state("");
  let error = $state("");
  let busy = $state(false);
  let result = $state<Schemas["RecommendResult"] | null>(null);

  $effect(() => {
    if (!open) return;
    people = [];
    note = "";
    error = "";
    result = null;
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!people.length) return;
    busy = true;
    const { data, error: err } = await api.POST("/books/{bookId}/recommend", {
      params: { path: { bookId } },
      body: { user_ids: people.map((p) => p.id), note: note.trim() || undefined },
    });
    busy = false;
    if (err) error = tError(err.code, err.message);
    else result = data;
  }
</script>

<Dialog {open} title={t("recommend.title", { title })} {onclose}>
  {#if result}
    <div class="stack">
      <Alert kind={result.sent ? "ok" : "info"}>
        {result.sent ? t("recommend.sent", { n: result.sent }) : t("recommend.none_sent")}
        {#if result.skipped}<span class="skipped">{t("recommend.skipped", { n: result.skipped })}</span>{/if}
      </Alert>
      <div class="row"><Button onclick={onclose}>{t("common.close")}</Button></div>
    </div>
  {:else}
    <form class="stack" onsubmit={submit}>
      <PeoplePicker bind:selected={people} label={t("recommend.to")} />
      <Field label={t("recommend.note")} bind:value={note} multiline rows={2} hint={t("recommend.note_hint")} />
      {#if error}<Alert kind="error">{error}</Alert>{/if}
      <div class="row">
        <Button type="submit" {busy} disabled={!people.length}>{t("recommend.submit")}</Button>
        <Button variant="secondary" onclick={onclose}>{t("common.cancel")}</Button>
      </div>
    </form>
  {/if}
</Dialog>

<style>
  .skipped {
    display: block;
  }
</style>
