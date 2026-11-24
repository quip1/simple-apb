<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // A list of books with the place each one is at (ADR-0056): Home's "your
  // other books" and the Your places page. A place this device saved but
  // hasn't sent yet is shown instead of the server's older one.
  import { api, type BookSummary } from "../api/client";
  import { ago, clock, percent } from "../lib/format";
  import { t } from "../lib/i18n";
  import { pending, unsentPlace } from "../lib/pending.svelte";
  import { openBook } from "../lib/player.svelte";
  import { link, navigate } from "../lib/router.svelte";
  import Cover from "./Cover.svelte";
  import Icon from "./Icon.svelte";
  import IconButton from "./IconButton.svelte";
  import ProgressBar from "./ProgressBar.svelte";

  let { books, details = false }: { books: BookSummary[]; details?: boolean } = $props();

  function place(b: BookSummary) {
    void pending.writes; // re-read when a waiting write is sent
    const unsent = unsentPlace(b.id, b.progress?.client_ts);
    const finished = unsent ? !!unsent.finished : !!b.progress?.finished_at;
    const pos = unsent?.position_ms ?? b.progress?.position_ms ?? 0;
    return {
      pos,
      finished,
      pct: finished ? 100 : percent(pos, b.duration_ms),
      unsent: !!unsent,
      device: b.progress?.device_name,
      when: b.progress?.updated_at,
    };
  }

  let starting = $state("");
  async function resume(id: string) {
    starting = id;
    const { data } = await api.GET("/books/{bookId}", { params: { path: { bookId: id } } });
    starting = "";
    if (data) {
      await openBook(data, { play: true });
      navigate("/player");
    }
  }
</script>

<ul class="places">
  {#each books as b (b.id)}
    {@const p = place(b)}
    <li>
      <a class="art" href={`/books/${b.id}`} use:link tabindex="-1" aria-hidden="true">
        <Cover src={b.cover_url} has={b.has_cover} title={b.title} size={128} />
      </a>
      <div class="text">
        <a class="title" href={`/books/${b.id}`} use:link>{b.title}</a>
        <span class="muted small">{b.authors.map((a) => a.name).join(", ")}</span>
        <ProgressBar value={p.pct} label={b.title} />
        <span class="small">
          {p.finished ? t("sync.finished") : t("places.position", { pos: clock(p.pos), total: clock(b.duration_ms) })}
          · {p.pct}%
        </span>
        {#if details}
          <span class="muted small" class:unsent={p.unsent}>
            {#if p.unsent}
              {t("places.unsent")}
            {:else if p.when && p.device}
              {t("places.saved_on", { device: p.device, when: ago(p.when) })}
            {:else if p.when}
              {t("places.saved", { when: ago(p.when) })}
            {/if}
          </span>
        {/if}
      </div>
      <IconButton primary label={t("places.resume", { title: b.title })} disabled={starting === b.id} onclick={() => void resume(b.id)}>
        <Icon name="play" />
      </IconButton>
    </li>
  {/each}
</ul>

<style>
  .places {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 0.75rem;
  }
  li {
    display: grid;
    grid-template-columns: 4rem 1fr auto;
    gap: 0.75rem;
    align-items: center;
    padding: 0.5rem;
    border: 1px solid var(--border);
    border-radius: 0.5rem;
    background: var(--surface);
  }
  .art {
    display: block;
    width: 4rem;
  }
  .text {
    display: grid;
    gap: 0.25rem;
    min-width: 0;
  }
  .title {
    font-weight: 600;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .small {
    font-size: 0.875rem;
  }
  .unsent {
    font-style: italic;
  }
</style>
