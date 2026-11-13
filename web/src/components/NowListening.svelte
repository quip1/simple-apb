<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // The top of Home (ADR-0051): the book you're on, large, with its place and
  // one button to carry on: the book in the player (live), or else the one
  // you played last. The card opens the player.
  import { api, type BookSummary } from "../api/client";
  import { duration, percent } from "../lib/format";
  import { t } from "../lib/i18n";
  import { currentChapter, openBook, player, toggle } from "../lib/player.svelte";
  import { link, navigate } from "../lib/router.svelte";
  import Button from "./Button.svelte";
  import Cover from "./Cover.svelte";
  import Icon from "./Icon.svelte";

  let { fallback }: { fallback?: BookSummary } = $props();

  const live = $derived(player.book);
  const chapter = $derived(currentChapter());
  const shown = $derived(
    live
      ? {
          label: t("home.now_listening"),
          id: live.id,
          title: live.title,
          sub: chapter?.title ?? live.authors.map((a) => a.name).join(", "),
          cover: live.cover_url,
          hasCover: live.has_cover,
          pos: player.displayMs,
          total: live.duration_ms,
        }
      : fallback
        ? {
            label: t("home.pick_up"),
            id: fallback.id,
            title: fallback.title,
            sub: fallback.authors.map((a) => a.name).join(", "),
            cover: fallback.cover_url,
            hasCover: fallback.has_cover,
            pos: fallback.progress?.position_ms ?? 0,
            total: fallback.duration_ms,
          }
        : null,
  );
  const pct = $derived(shown ? percent(shown.pos, shown.total) : 0);

  let starting = $state(false);
  async function carryOn() {
    if (live) {
      toggle();
      return;
    }
    if (!fallback) return;
    starting = true;
    const { data } = await api.GET("/books/{bookId}", { params: { path: { bookId: fallback.id } } });
    starting = false;
    if (data) {
      await openBook(data, { play: true });
      navigate("/player");
    }
  }
</script>

{#if shown}
  <section class="now card" aria-labelledby="now-title">
    <a class="top" href={live ? "/player" : `/books/${shown.id}`} use:link>
      <div class="art"><Cover src={shown.cover} has={shown.hasCover} title={shown.title} size={256} eager /></div>
      <div class="text">
        <span class="label">{shown.label}</span>
        <h2 id="now-title">{shown.title}</h2>
        <span class="muted">{shown.sub}</span>
      </div>
    </a>
    <div class="bar" aria-hidden="true"><div class="fill" style="width: {pct}%"></div></div>
    <p class="muted left">{t("player.book_left", { pct, left: duration(Math.max(0, shown.total - shown.pos)) })}</p>
    <Button busy={starting} onclick={() => void carryOn()}>
      <span class="carry">
        <Icon name={live && player.playing ? "pause" : "play"} />
        {live && player.playing ? t("player.pause") : shown.pos > 0 ? t("book.resume") : t("book.play")}
      </span>
    </Button>
  </section>
{/if}

<style>
  .now {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    max-width: 40rem;
  }
  .top {
    display: flex;
    gap: 1.25rem;
    align-items: center;
    color: inherit;
    text-decoration: none;
  }
  .art {
    width: 9rem;
    flex: none;
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    min-width: 0;
  }
  .label {
    color: var(--accent);
    font-weight: 600;
  }
  h2 {
    margin: 0;
    font-size: 1.5rem;
    overflow-wrap: anywhere;
  }
  .bar {
    height: 6px;
    border-radius: 3px;
    background: var(--surface-2);
    border: 1px solid var(--border);
    overflow: hidden;
  }
  .fill {
    height: 100%;
    background: var(--accent);
  }
  .left {
    margin: 0;
  }
  .carry {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
  }
</style>
