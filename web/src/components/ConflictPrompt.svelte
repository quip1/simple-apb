<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // "Jump to 4:12:30 from <device>?" — the user's choice is final (brief §6.8).
  import { clock } from "../lib/format";
  import { t } from "../lib/i18n";
  import { player, resolveConflict } from "../lib/player.svelte";
  import Button from "./Button.svelte";
  import Dialog from "./Dialog.svelte";

  let busy = $state(false);
  const c = $derived(player.conflict);

  async function choose(jump: boolean) {
    busy = true;
    try {
      await resolveConflict(jump);
    } finally {
      busy = false;
    }
  }
</script>

<!-- Closing without choosing keeps this device's position. -->
<Dialog open={!!c} title={t("player.conflict_title")} onclose={() => c && !busy && choose(false)}>
  {#if c}
    <p>
      {t("player.conflict_body", {
        time: clock(c.server.position_ms),
        device: c.server.device_name || t("player.another_device"),
        mine: clock(player.positionMs),
      })}
    </p>
    <div class="row">
      <Button {busy} onclick={() => choose(true)}>{t("player.conflict_jump", { time: clock(c.server.position_ms) })}</Button>
      <Button variant="secondary" {busy} onclick={() => choose(false)}
        >{t("player.conflict_stay", { time: clock(player.positionMs) })}</Button
      >
    </div>
  {/if}
</Dialog>
