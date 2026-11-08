<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Star rating: read-only display, or an input (a radio group, so keyboard
  // and screen readers get the standard behaviour).
  import { t } from "../lib/i18n";

  let {
    value = $bindable(0),
    input = false,
    label = "",
    size = "1.1rem",
  }: { value?: number; input?: boolean; label?: string; size?: string } = $props();

  const name = `stars-${Math.random().toString(36).slice(2, 9)}`;
  let hover = $state(0);
  const shown = $derived(hover || value);
</script>

{#if input}
  <fieldset class="stars input" style:--size={size}>
    <legend>{label || t("reviews.your_rating")}</legend>
    <div class="row-stars" role="presentation" onmouseleave={() => (hover = 0)}>
      {#each [1, 2, 3, 4, 5] as n (n)}
        <label class:on={n <= shown} onmouseenter={() => (hover = n)}>
          <input type="radio" {name} value={n} bind:group={value} />
          <span aria-hidden="true">★</span>
          <span class="sr-only">{t("reviews.stars", { n })}</span>
        </label>
      {/each}
    </div>
  </fieldset>
{:else}
  <span class="stars" style:--size={size} role="img" aria-label={t("reviews.stars_of", { n: Math.round(value * 10) / 10 })}>
    {#each [1, 2, 3, 4, 5] as n (n)}
      <span class="star" aria-hidden="true" style:--fill="{Math.max(0, Math.min(1, value - n + 1)) * 100}%">★</span>
    {/each}
  </span>
{/if}

<style>
  .stars {
    display: inline-flex;
    font-size: var(--size);
    line-height: 1;
    letter-spacing: 0.05em;
  }
  /* Filled part in the accent colour, every star outlined, so empty and
     full stars differ in e-ink too (where the accent is plain black). */
  .star {
    background: linear-gradient(90deg, var(--accent) var(--fill), transparent var(--fill));
    background-clip: text;
    -webkit-background-clip: text;
    color: transparent;
    -webkit-text-stroke: 1px var(--accent);
  }
  fieldset {
    border: 0;
    margin: 0;
    padding: 0;
    flex-direction: column;
    align-items: flex-start;
    gap: 0.25rem;
  }
  legend {
    padding: 0;
    margin-bottom: 0.25rem;
  }
  .row-stars {
    display: flex;
  }
  .input label {
    position: relative;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 44px;
    min-height: 44px;
    cursor: pointer;
    color: transparent;
    -webkit-text-stroke: 1.5px var(--text-muted);
    font-size: calc(var(--size) * 1.6);
  }
  .input label.on {
    color: var(--accent);
    -webkit-text-stroke-color: var(--accent);
  }
  .input input {
    position: absolute;
    opacity: 0;
    inset: 0;
    margin: 0;
    cursor: pointer;
  }
  .input label:has(input:focus-visible) {
    outline: 3px solid var(--focus);
    outline-offset: 2px;
    border-radius: var(--radius-sm);
  }
  @media (forced-colors: active) {
    .star {
      color: CanvasText;
      background: none;
    }
    .input label.on {
      color: Highlight;
    }
  }
</style>
