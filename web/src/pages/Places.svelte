<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Your places (ADR-0056): every book you've started, the place each is at
  // and which device saved it, and whether this device still has places to send.
  import { api, type BookSummary } from "../api/client";
  import Alert from "../components/Alert.svelte";
  import BookPlaces from "../components/BookPlaces.svelte";
  import Button from "../components/Button.svelte";
  import { t } from "../lib/i18n";
  import { live } from "../lib/live.svelte";
  import { flushPending, pending, refreshPending } from "../lib/pending.svelte";
  import { session } from "../lib/session.svelte";

  let books = $state<BookSummary[]>([]);
  let loaded = $state(false);
  let failed = $state(false);
  let sending = $state(false);
  const waiting = $derived(Object.keys(pending.writes).length);

  async function all(shelf: "in_progress" | "finished"): Promise<BookSummary[]> {
    const out: BookSummary[] = [];
    let cursor: string | undefined;
    do {
      const { data, error } = await api.GET("/library", {
        params: { query: { shelf, sort: "recent_played", limit: 200, cursor } },
      });
      if (error) throw new Error(error.code);
      out.push(...data.items);
      cursor = data.next_cursor ?? undefined;
    } while (cursor && out.length < 2000);
    return out;
  }

  async function load() {
    refreshPending();
    try {
      const [going, done] = await Promise.all([all("in_progress"), all("finished")]);
      const at = (b: BookSummary) => Date.parse(b.progress?.updated_at ?? "") || 0;
      books = [...going, ...done].sort((a, b) => at(b) - at(a));
      failed = false;
    } catch {
      failed = true;
    }
    loaded = true;
  }

  $effect(() => {
    void live.tick;
    void load();
  });

  async function sendNow() {
    sending = true;
    await flushPending();
    sending = false;
    void load();
  }
</script>

<svelte:head><title>{t("sync.title")} · {session.instance?.name ?? ""}</title></svelte:head>

<div class="stack">
  <h1>{t("sync.title")}</h1>
  <p class="muted">{t("sync.intro")}</p>
  {#if waiting}
    <Alert kind="info">
      {t("sync.waiting", { n: waiting })}
      <Button variant="ghost" busy={sending} onclick={() => void sendNow()}>{t("sync.send_now")}</Button>
    </Alert>
  {:else if loaded && !failed}
    <p role="status">{t("sync.all_sent")}</p>
  {/if}
  {#if failed}
    <Alert kind="error">{t("error.generic")}</Alert>
  {/if}
  {#if !loaded}
    <p class="muted" role="status">{t("common.loading")}</p>
  {:else if !books.length && !failed}
    <p class="muted">{t("sync.empty")}</p>
  {:else}
    <BookPlaces {books} details />
  {/if}
</div>
