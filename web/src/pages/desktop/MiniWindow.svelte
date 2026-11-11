<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // The Windows app's mini player (brief §10): a small always-on-top window
  // with the cover and the controls. It plays nothing itself: it shows the
  // main window's player and sends it commands.
  import Cover from "../../components/Cover.svelte";
  import Icon from "../../components/Icon.svelte";
  import IconButton from "../../components/IconButton.svelte";
  import { currentPlayer, sendCommand, type PlayerSnapshot } from "../../lib/desktop.svelte";
  import { clock } from "../../lib/format";
  import { t } from "../../lib/i18n";
  import { prefs } from "../../lib/prefs.svelte";

  let s = $state<PlayerSnapshot | null>(null);

  $effect(() => {
    void currentPlayer().then((p) => (s = p && "playing" in p ? p : null));
    const on = (e: Event) => (s = (e as CustomEvent<PlayerSnapshot>).detail);
    window.addEventListener("sapb:player", on);
    return () => window.removeEventListener("sapb:player", on);
  });
</script>

<svelte:head><title>{t("desktop.mini.title")}</title></svelte:head>

<main class="mini">
  {#if s?.book_id}
    <div class="art"><Cover src={s.cover ?? ""} has={!!s.cover} title={s.title ?? ""} size={128} /></div>
    <div class="text">
      <span class="title">{s.title}</span>
      <span class="muted">{clock(s.position_ms)} / {clock(s.duration_ms)}</span>
    </div>
    <div class="controls">
      <IconButton label={t("player.skip_back", { s: prefs.skipBackS })} onclick={() => sendCommand("back")}><Icon name="back" /></IconButton
      >
      <IconButton primary label={s.playing ? t("player.pause") : t("player.play")} onclick={() => sendCommand("toggle")}>
        <Icon name={s.playing ? "pause" : "play"} size={28} />
      </IconButton>
      <IconButton label={t("player.skip_forward", { s: prefs.skipForwardS })} onclick={() => sendCommand("forward")}
        ><Icon name="forward" /></IconButton
      >
    </div>
  {:else}
    <p class="muted">{t("player.nothing_playing")}</p>
  {/if}
</main>

<style>
  .mini {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.5rem 0.75rem;
    height: 100vh;
    box-sizing: border-box;
  }
  .art {
    width: 72px;
    flex: none;
  }
  .text {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  .title {
    font-weight: 600;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .controls {
    display: flex;
    gap: 0.25rem;
  }
</style>
