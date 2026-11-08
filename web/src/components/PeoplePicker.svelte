<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Find people in your groups by name (an accessible combobox-style list).
  import { api, type Schemas } from "../api/client";
  import { t } from "../lib/i18n";
  import Icon from "./Icon.svelte";
  import IconButton from "./IconButton.svelte";

  type Person = Schemas["UserRef"];

  let { selected = $bindable<Person[]>([]), max = 20, label }: { selected?: Person[]; max?: number; label: string } = $props();

  const id = `pp-${Math.random().toString(36).slice(2, 9)}`;
  let q = $state("");
  let results = $state<Person[]>([]);
  let searched = $state(false);
  let timer: ReturnType<typeof setTimeout> | undefined;

  function onInput() {
    clearTimeout(timer);
    const term = q.trim();
    if (term.length < 2) {
      results = [];
      searched = false;
      return;
    }
    timer = setTimeout(async () => {
      const { data } = await api.GET("/people", { params: { query: { q: term } } });
      results = data ?? [];
      searched = true;
    }, 200);
  }

  function add(p: Person) {
    if (!selected.some((s) => s.id === p.id) && selected.length < max) selected = [...selected, p];
    q = "";
    results = [];
    searched = false;
  }
  function drop(p: Person) {
    selected = selected.filter((s) => s.id !== p.id);
  }
  const shown = $derived(results.filter((r) => !selected.some((s) => s.id === r.id)));
</script>

<div class="picker stack">
  <label for={id}>{label}</label>
  {#if selected.length}
    <ul class="chips" aria-label={t("people.selected")}>
      {#each selected as p (p.id)}
        <li>
          <span>{p.display_name}</span>
          <IconButton label={t("people.remove", { name: p.display_name })} onclick={() => drop(p)}
            ><Icon name="close" size={18} /></IconButton
          >
        </li>
      {/each}
    </ul>
  {/if}
  {#if selected.length < max}
    <input
      {id}
      class="text"
      type="search"
      bind:value={q}
      oninput={onInput}
      placeholder={t("people.placeholder")}
      autocomplete="off"
      maxlength="100"
      aria-describedby="{id}-hint"
    />
    <p id="{id}-hint" class="muted hint">{t("people.hint")}</p>
    {#if shown.length}
      <ul class="results" aria-label={t("people.results")}>
        {#each shown as p (p.id)}
          <li>
            <button type="button" onclick={() => add(p)}>
              <span>{p.display_name}</span><span class="muted">@{p.username}</span>
            </button>
          </li>
        {/each}
      </ul>
    {:else if searched}
      <p class="muted" role="status">{t("people.none")}</p>
    {/if}
  {/if}
</div>

<style>
  .picker {
    gap: 0.4rem;
  }
  .hint {
    margin: 0;
    font-size: 0.9rem;
  }
  .chips,
  .results {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
  }
  .chips li {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
    padding-inline-start: 0.75rem;
    border: 1px solid var(--border);
    border-radius: 999px;
    background: var(--surface-2);
  }
  .results {
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
  }
  .results li + li {
    border-top: 1px solid var(--border);
  }
  .results button {
    display: flex;
    gap: 0.5rem;
    width: 100%;
    min-height: 48px;
    align-items: center;
    padding: 0 0.75rem;
    border: 0;
    background: transparent;
    color: var(--text);
    font: inherit;
    text-align: start;
    cursor: pointer;
  }
  .results button:hover,
  .results button:focus-visible {
    background: var(--surface-2);
  }
</style>
