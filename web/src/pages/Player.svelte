<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Now playing (brief §7.4 player): big cover, chapter title, chapter
  // progress plus overall progress, back/forward/play, chapter skip, speed,
  // sleep timer, chapter list, bookmarks and stream quality.
  import Alert from "../components/Alert.svelte";
  import Bookmarks from "../components/Bookmarks.svelte";
  import Button from "../components/Button.svelte";
  import ChapterList from "../components/ChapterList.svelte";
  import Cover from "../components/Cover.svelte";
  import Dialog from "../components/Dialog.svelte";
  import Icon from "../components/Icon.svelte";
  import IconButton from "../components/IconButton.svelte";
  import ProgressBar from "../components/ProgressBar.svelte";
  import { clock, duration, percent, spokenClock } from "../lib/format";
  import { t } from "../lib/i18n";
  import {
    chapterIndexAt,
    nextChapter,
    player,
    previousChapter,
    seekTo,
    setSleep,
    setSpeed,
    skip,
    SLEEP_MINUTES,
    SPEEDS,
    switchProfile,
    toggle,
  } from "../lib/player.svelte";
  import { prefs, savePrefs } from "../lib/prefs.svelte";
  import { link } from "../lib/router.svelte";
  import { session } from "../lib/session.svelte";

  const book = $derived(player.book);
  const chIdx = $derived(chapterIndexAt(book, player.displayMs));
  const chapter = $derived(book && chIdx >= 0 ? book.chapters[chIdx] : null);
  // Without chapters, the "chapter" bar is the whole book.
  const chStart = $derived(chapter?.start_ms ?? 0);
  const chEnd = $derived(chapter?.end_ms ?? book?.duration_ms ?? 0);
  const chLen = $derived(Math.max(1, chEnd - chStart));

  // While dragging, the slider shows the drag position, not playback.
  let dragging = $state<number | null>(null);
  const inChapter = $derived(dragging ?? Math.min(chLen, Math.max(0, player.displayMs - chStart)));

  function commitSeek() {
    if (dragging === null) return;
    void seekTo(chStart + dragging);
    dragging = null;
  }

  let chaptersOpen = $state(false);
  let bookmarksOpen = $state(false);

  // Sleep timer countdown (coarse; e-ink friendly).
  let now = $state(Date.now());
  $effect(() => {
    if (player.sleep?.kind !== "time") return;
    const id = setInterval(() => (now = Date.now()), 15_000);
    return () => clearInterval(id);
  });
  const sleepLabel = $derived.by(() => {
    const s = player.sleep;
    if (!s) return t("player.sleep_off");
    if (s.kind === "chapter") return t("player.sleep_end_of_chapter");
    return t("player.sleep_left", { m: Math.max(1, Math.ceil((s.endsAt - now) / 60000)) });
  });

  function onSleep(v: string) {
    now = Date.now();
    setSleep(v === "off" ? null : v === "chapter" ? "chapter" : Number(v));
  }

  function onSpeed(v: string) {
    setSpeed(Number(v));
    savePrefs();
  }

  const speeds = $derived(SPEEDS.includes(player.speed) ? SPEEDS : [...SPEEDS, player.speed].sort((a, b) => a - b));
</script>

<svelte:head><title>{book?.title ?? t("nav.player")} · {session.instance?.name ?? ""}</title></svelte:head>

{#if !book}
  <div class="stack">
    <h1>{t("nav.player")}</h1>
    <p class="muted">{t("player.nothing_playing")}</p>
    <p><a href="/library" use:link>{t("home.browse_library")}</a></p>
  </div>
{:else}
  <div class="player" class:left={prefs.leftHanded}>
    <div class="art"><Cover src={book.cover_url} has={book.has_cover} title={book.title} size={1024} eager /></div>

    <div class="panel stack">
      <header class="titles">
        <h1><a href="/books/{book.id}" use:link>{book.title}</a></h1>
        <p class="muted">{book.authors.map((a) => a.name).join(", ")}</p>
        {#if chapter}<p class="chapter">{chapter.title}</p>{/if}
      </header>

      {#if player.error}<Alert kind="error">{player.error}</Alert>{/if}

      <div class="scrub">
        <label class="sr-only" for="scrub">{t("player.chapter_position")}</label>
        <input
          id="scrub"
          type="range"
          min="0"
          max={chLen}
          step="1000"
          value={inChapter}
          aria-valuetext={t("player.position_valuetext", { at: spokenClock(inChapter), total: spokenClock(chLen) })}
          oninput={(e) => (dragging = Number(e.currentTarget.value))}
          onchange={commitSeek}
        />
        <div class="times muted" aria-hidden="true">
          <span>{clock(inChapter)}</span>
          <span>{player.buffering ? t("player.buffering") : ""}</span>
          <span>−{clock(chLen - inChapter)}</span>
        </div>
      </div>

      <div class="controls" role="group" aria-label={t("player.controls")}>
        <IconButton label={t("player.previous_chapter")} onclick={previousChapter}><Icon name="prev" /></IconButton>
        <IconButton label={t("player.skip_back", { s: prefs.skipBackS })} onclick={() => skip(-prefs.skipBackS)}>
          <Icon name="back" /><span aria-hidden="true">{prefs.skipBackS}</span>
        </IconButton>
        <IconButton primary label={player.playing ? t("player.pause") : t("player.play")} onclick={toggle}>
          <Icon name={player.playing ? "pause" : "play"} size={32} />
        </IconButton>
        <IconButton label={t("player.skip_forward", { s: prefs.skipForwardS })} onclick={() => skip(prefs.skipForwardS)}>
          <span aria-hidden="true">{prefs.skipForwardS}</span><Icon name="forward" />
        </IconButton>
        <IconButton
          label={t("player.next_chapter")}
          disabled={!book.chapters.length || chIdx >= book.chapters.length - 1}
          onclick={nextChapter}><Icon name="next" /></IconButton
        >
      </div>

      <div class="overall">
        <ProgressBar value={percent(player.displayMs, book.duration_ms)} label={t("player.book_progress")} />
        <p class="muted">
          {t("player.book_left", {
            pct: percent(player.displayMs, book.duration_ms),
            left: duration(book.duration_ms - player.displayMs),
          })}
        </p>
      </div>

      <div class="options">
        <label>
          <span>{t("player.speed")}</span>
          <select value={player.speed} onchange={(e) => onSpeed(e.currentTarget.value)}>
            {#each speeds as s (s)}<option value={s}>{t("player.speed_value", { s })}</option>{/each}
          </select>
        </label>
        <label>
          <span>{t("player.sleep")}</span>
          <select
            value={player.sleep ? (player.sleep.kind === "chapter" ? "chapter" : "custom") : "off"}
            onchange={(e) => onSleep(e.currentTarget.value)}
          >
            <option value="off">{t("player.sleep_off")}</option>
            {#if player.sleep?.kind === "time"}<option value="custom" disabled>{sleepLabel}</option>{/if}
            {#each SLEEP_MINUTES as m (m)}<option value={m}>{t("player.sleep_minutes", { m })}</option>{/each}
            <option value="chapter">{t("player.sleep_end_of_chapter")}</option>
          </select>
        </label>
        <label>
          <span>{t("player.quality")}</span>
          <select value={player.profile} onchange={(e) => switchProfile(e.currentTarget.value as "original" | "medium" | "low")}>
            <option value="original">{t("player.quality_original")}</option>
            <option value="medium">{t("player.quality_medium")}</option>
            <option value="low">{t("player.quality_low")}</option>
          </select>
        </label>
      </div>
      {#if player.sleep}<p class="muted" role="status">{sleepLabel}</p>{/if}

      <div class="row more">
        {#if book.chapters.length}
          <Button variant="secondary" onclick={() => (chaptersOpen = true)}>{t("chapters.title", { n: book.chapters.length })}</Button>
        {/if}
        <Button variant="secondary" onclick={() => (bookmarksOpen = true)}>
          <span class="with-icon"><Icon name="bookmark" size={20} />{t("bookmarks.title")}</span>
        </Button>
      </div>
    </div>
  </div>

  <Dialog open={chaptersOpen} title={t("chapters.title", { n: book.chapters.length })} onclose={() => (chaptersOpen = false)}>
    <ChapterList
      chapters={book.chapters}
      current={chIdx}
      onselect={(c) => {
        void seekTo(c.start_ms);
        chaptersOpen = false;
      }}
    />
    <Button variant="secondary" onclick={() => (chaptersOpen = false)}>{t("common.close")}</Button>
  </Dialog>

  <Dialog open={bookmarksOpen} title={t("bookmarks.title")} onclose={() => (bookmarksOpen = false)}>
    {#if bookmarksOpen}
      <Bookmarks
        bookId={book.id}
        addAt={() => player.positionMs}
        onjump={(ms) => {
          void seekTo(ms);
          bookmarksOpen = false;
        }}
      />
    {/if}
    <Button variant="secondary" onclick={() => (bookmarksOpen = false)}>{t("common.close")}</Button>
  </Dialog>
{/if}

<style>
  .player {
    display: grid;
    grid-template-columns: minmax(14rem, 26rem) minmax(0, 32rem);
    justify-content: center;
    gap: 2.5rem;
    align-items: center;
  }
  .art {
    width: 100%;
  }
  .titles h1 {
    font-size: 1.5rem;
    margin: 0;
  }
  .titles h1 a {
    color: var(--text);
    text-decoration: none;
  }
  .titles p {
    margin: 0.25rem 0 0;
  }
  .chapter {
    font-weight: 600;
  }
  .times {
    display: flex;
    justify-content: space-between;
    font-variant-numeric: tabular-nums;
    font-size: 0.9rem;
  }
  .controls {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 0.25rem;
  }
  .left .controls,
  .left .options,
  .left .more {
    flex-direction: row-reverse;
  }
  .overall p {
    margin: 0.4rem 0 0;
    font-size: 0.9rem;
  }
  .options {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
  }
  .options label {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    font-size: 0.875rem;
    color: var(--text-muted);
    flex: 1 1 8rem;
  }
  .with-icon {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
  }
  @media (max-width: 48rem) {
    .player {
      grid-template-columns: 1fr;
      gap: 1.25rem;
    }
    .art {
      width: min(20rem, 75vw);
      margin: 0 auto;
    }
    /* Reachable layout: controls sit in the lower part of the screen. */
    .panel {
      display: flex;
      flex-direction: column;
    }
  }
</style>
