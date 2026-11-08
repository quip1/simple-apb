<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Modal dialog on the native <dialog> element (focus trap, Esc to close).
  import type { Snippet } from "svelte";

  let { open, title, onclose, children }: { open: boolean; title: string; onclose: () => void; children: Snippet } = $props();

  let node: HTMLDialogElement | undefined = $state();
  const id = `dlg-${Math.random().toString(36).slice(2)}`;

  $effect(() => {
    if (!node) return;
    if (open && !node.open) node.showModal();
    else if (!open && node.open) node.close();
  });

  function backdrop(e: MouseEvent) {
    if (e.target === node) onclose();
  }
</script>

<!-- Clicking the backdrop closes; Esc is handled by <dialog> itself. -->
<dialog bind:this={node} aria-labelledby={id} {onclose} onclick={backdrop}>
  <div class="body stack">
    <h2 {id}>{title}</h2>
    {@render children()}
  </div>
</dialog>

<style>
  dialog {
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
    padding: 0;
    width: min(32rem, calc(100vw - 2rem));
    max-height: calc(100vh - 2rem);
    box-shadow: var(--shadow);
  }
  dialog::backdrop {
    background: rgb(0 0 0 / 0.6);
  }
  :global([data-theme="eink"]) dialog::backdrop {
    background: rgb(255 255 255 / 0.85);
  }
  .body {
    padding: 1.25rem;
  }
</style>
