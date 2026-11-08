<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import type { Snippet } from "svelte";

  let {
    variant = "primary",
    type = "button",
    disabled = false,
    busy = false,
    onclick,
    children,
  }: {
    variant?: "primary" | "secondary" | "danger" | "ghost";
    type?: "button" | "submit";
    disabled?: boolean;
    busy?: boolean;
    onclick?: (e: MouseEvent) => void;
    children: Snippet;
  } = $props();
</script>

<button class="btn {variant}" {type} disabled={disabled || busy} aria-busy={busy} {onclick}>
  {@render children()}
</button>

<style>
  .btn {
    min-height: 48px; /* brief: touch targets ≥ 48 dp */
    min-width: 48px;
    padding: 0 1.25rem;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
    font: inherit;
    font-weight: 600;
    cursor: pointer;
    transition:
      background var(--motion),
      border-color var(--motion),
      opacity var(--motion);
  }
  .primary {
    background: var(--accent);
    color: var(--accent-text);
    border-color: var(--accent);
  }
  .secondary {
    background: var(--surface-2);
    color: var(--text);
  }
  .danger {
    background: var(--danger-bg);
    color: var(--danger);
    border-color: var(--danger);
  }
  .ghost {
    background: transparent;
    color: var(--text);
    border-color: transparent;
  }
  .btn:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }
  /* E-ink: busy state is shown by text, not by a spinner. */
  .btn[aria-busy="true"]::after {
    content: " …";
  }
</style>
