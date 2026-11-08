<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import type { Snippet } from "svelte";
  let { kind = "info", children }: { kind?: "info" | "error" | "ok"; children: Snippet } = $props();
</script>

<!-- Errors interrupt screen readers; other notices are polite. -->
<div class="alert {kind}" role={kind === "error" ? "alert" : "status"}>
  <span class="icon" aria-hidden="true">{kind === "error" ? "⚠" : kind === "ok" ? "✓" : "ℹ"}</span>
  <div>{@render children()}</div>
</div>

<style>
  .alert {
    display: flex;
    gap: 0.6rem;
    padding: 0.8rem 1rem;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
    background: var(--surface-2);
  }
  .error {
    border-color: var(--danger);
    background: var(--danger-bg);
  }
  .ok {
    border-color: var(--ok);
    background: var(--ok-bg);
  }
  .icon {
    font-weight: 700;
  }
</style>
