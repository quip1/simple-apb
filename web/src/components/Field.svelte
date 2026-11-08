<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  let {
    label,
    value = $bindable(""),
    type = "text",
    hint = "",
    error = "",
    autocomplete,
    required = false,
    inputmode,
    placeholder = "",
    id = `f-${Math.random().toString(36).slice(2, 9)}`,
    multiline = false,
    rows = 6,
  }: {
    label: string;
    value?: string;
    type?: string;
    hint?: string;
    error?: string;
    autocomplete?: HTMLInputElement["autocomplete"];
    required?: boolean;
    inputmode?: "none" | "text" | "search" | "tel" | "url" | "email" | "numeric" | "decimal";
    placeholder?: string;
    id?: string;
    multiline?: boolean;
    rows?: number;
  } = $props();

  const describedBy = $derived([hint && `${id}-hint`, error && `${id}-err`].filter(Boolean).join(" ") || undefined);
</script>

<div class="field">
  <label for={id}
    >{label}{#if required}<span aria-hidden="true"> *</span>{/if}</label
  >
  {#if multiline}
    <textarea {id} bind:value {rows} {required} aria-invalid={!!error} aria-describedby={describedBy} {placeholder}></textarea>
  {:else}
    <input
      {id}
      {type}
      bind:value
      {autocomplete}
      {required}
      {inputmode}
      {placeholder}
      aria-invalid={!!error}
      aria-describedby={describedBy}
    />
  {/if}
  {#if hint}<p class="hint" id="{id}-hint">{hint}</p>{/if}
  {#if error}<p class="error" id="{id}-err" role="alert">{error}</p>{/if}
</div>

<style>
  .field {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }
  label {
    font-weight: 600;
  }
  input,
  textarea {
    min-height: 48px;
    padding: 0.6rem 0.8rem;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
    background: var(--surface-2);
    color: var(--text);
    font: inherit;
    width: 100%;
  }
  textarea {
    resize: vertical;
  }
  input[aria-invalid="true"],
  textarea[aria-invalid="true"] {
    border-color: var(--danger);
    border-width: 2px; /* not colour alone */
  }
  .hint {
    margin: 0;
    color: var(--text-muted);
    font-size: 0.9rem;
  }
  .error {
    margin: 0;
    color: var(--danger);
    font-weight: 600;
  }
  .error::before {
    content: "⚠ "; /* not colour alone */
  }
</style>
