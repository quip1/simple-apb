<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  let { value, label }: { value: number; label: string } = $props();
</script>

<div class="bar" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={value} aria-label={label}>
  <div class="fill" style="width: {value}%"></div>
</div>

<style>
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
</style>
