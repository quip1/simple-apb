<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import type { Snippet } from "svelte";

  let {
    label,
    onclick,
    primary = false,
    pressed,
    disabled = false,
    children,
  }: {
    label: string;
    onclick: () => void;
    primary?: boolean;
    pressed?: boolean;
    disabled?: boolean;
    children: Snippet;
  } = $props();
</script>

<button type="button" class="ib" class:primary aria-label={label} title={label} aria-pressed={pressed} {disabled} {onclick}>
  {@render children()}
</button>

<style>
  .ib {
    min-width: 48px;
    min-height: 48px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 0.2rem;
    border-radius: 999px;
    border: 1px solid transparent;
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
    padding: 0 0.5rem;
  }
  .ib:hover:not(:disabled) {
    background: var(--surface-2);
  }
  .ib:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }
  .primary {
    background: var(--accent);
    color: var(--accent-text);
    min-width: 56px;
    min-height: 56px;
  }
  .primary:hover:not(:disabled) {
    background: var(--accent);
    filter: brightness(1.08);
  }
  [aria-pressed="true"] {
    border-color: var(--border);
    background: var(--surface-2);
  }
  :global([data-theme="eink"]) .ib {
    border-color: var(--border);
  }
</style>
