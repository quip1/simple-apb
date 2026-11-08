<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // The mini-player bar (brief §11: Home, Library and Now playing via this bar).
  import { percent } from "../lib/format";
  import { t } from "../lib/i18n";
  import { player, currentChapter, skip, toggle } from "../lib/player.svelte";
  import { prefs } from "../lib/prefs.svelte";
  import { link } from "../lib/router.svelte";
  import Cover from "./Cover.svelte";
  import Icon from "./Icon.svelte";
  import IconButton from "./IconButton.svelte";

  const book = $derived(player.book!);
  const chapter = $derived(currentChapter());
  const pct = $derived(percent(player.displayMs, book.duration_ms));
</script>

<aside class="mini" class:left={prefs.leftHanded} aria-label={t("player.now_playing")}>
  <div class="track" style="width: {pct}%" aria-hidden="true"></div>
  <a class="info" href="/player" use:link>
    <div class="art"><Cover src={book.cover_url} has={book.has_cover} title={book.title} size={128} /></div>
    <div class="text">
      <span class="title">{book.title}</span>
      <span class="muted sub">{chapter?.title ?? book.authors.map((a) => a.name).join(", ")}</span>
    </div>
  </a>
  <div class="controls">
    <IconButton label={t("player.skip_back", { s: prefs.skipBackS })} onclick={() => skip(-prefs.skipBackS)}>
      <Icon name="back" />
    </IconButton>
    <IconButton primary label={player.playing ? t("player.pause") : t("player.play")} onclick={toggle}>
      <Icon name={player.playing ? "pause" : "play"} size={28} />
    </IconButton>
  </div>
</aside>

<style>
  .mini {
    position: fixed;
    inset: auto 0 0 0;
    z-index: 10;
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.5rem max(0.75rem, env(safe-area-inset-right)) calc(0.5rem + env(safe-area-inset-bottom))
      max(0.75rem, env(safe-area-inset-left));
    background: var(--surface);
    border-top: 1px solid var(--border);
    box-shadow: var(--shadow);
  }
  .left {
    flex-direction: row-reverse;
  }
  .track {
    position: absolute;
    top: -1px;
    left: 0;
    height: 2px;
    background: var(--accent);
  }
  .info {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    min-width: 0;
    flex: 1;
    color: inherit;
    text-decoration: none;
    min-height: 48px;
  }
  .art {
    width: 3rem;
    flex: none;
  }
  .text {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }
  .title,
  .sub {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .title {
    font-weight: 600;
  }
  .sub {
    font-size: 0.85rem;
  }
  .controls {
    display: flex;
    align-items: center;
    gap: 0.25rem;
  }
</style>
