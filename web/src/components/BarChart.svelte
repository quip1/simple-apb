<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts" module>
  export interface Bar {
    /** Full label for tooltips and the table ("Mon 3 March"). */
    label: string;
    /** Axis tick text, or "" to leave the tick out. */
    tick: string;
    value: number;
  }
</script>

<script lang="ts">
  // Single-series bar chart (listening time). One hue, thin bars with 4 px
  // rounded data-ends on the baseline, 2 px gaps, recessive grid, a hover/focus
  // tooltip, and a table view so the numbers never depend on the picture.
  import { t } from "../lib/i18n";

  let {
    bars,
    title,
    format,
  }: {
    bars: Bar[];
    title: string;
    format: (v: number) => string;
  } = $props();

  // Drawn at the real width, so text stays 11 px on a phone.
  let width = $state(640);
  const W = $derived(Math.max(280, Math.round(width)));
  const H = 200;
  const PAD = { top: 12, right: 8, bottom: 26, left: 52 };
  const GAP = 2;
  const plotW = $derived(W - PAD.left - PAD.right);
  const plotH = H - PAD.top - PAD.bottom;

  // A friendly axis maximum in whole minutes/hours.
  const max = $derived.by(() => {
    const m = Math.max(0, ...bars.map((b) => b.value));
    if (m === 0) return 3600_000;
    const steps = [1, 2, 5, 10, 15, 30, 60, 120, 180, 240, 360, 480, 720, 1440, 2880, 5760].map((x) => x * 60_000);
    const per = steps.find((s) => s * 3 >= m) ?? Math.ceil(m / 3);
    return per * 3;
  });
  const grid = $derived([1, 2, 3].map((i) => (max / 3) * i));
  const step = $derived(plotW / Math.max(1, bars.length));
  const barW = $derived(Math.max(1, step - GAP));

  function y(v: number): number {
    return PAD.top + plotH - (v / max) * plotH;
  }

  /** A bar with a 4 px rounded top, square on the baseline. */
  function barPath(x: number, v: number): string {
    const top = y(v);
    const h = PAD.top + plotH - top;
    if (h <= 0) return "";
    const r = Math.min(4, barW / 2, h);
    const b = PAD.top + plotH;
    return `M${x},${b}V${top + r}Q${x},${top} ${x + r},${top}H${x + barW - r}Q${x + barW},${top} ${x + barW},${top + r}V${b}Z`;
  }

  let hover = $state<number | null>(null);
  let showTable = $state(false);
  const id = `chart-${Math.random().toString(36).slice(2)}`;
</script>

<figure class="chart" aria-labelledby="{id}-t">
  <figcaption class="head">
    <h3 id="{id}-t">{title}</h3>
    <button type="button" class="toggle" aria-pressed={showTable} onclick={() => (showTable = !showTable)}>
      {showTable ? t("stats.show_chart") : t("stats.show_table")}
    </button>
  </figcaption>

  {#if showTable}
    <div class="table-wrap">
      <table>
        <thead><tr><th scope="col">{t("stats.col_period")}</th><th scope="col">{t("stats.col_listening")}</th></tr></thead>
        <tbody>
          {#each bars as b, i (i)}<tr><td>{b.label}</td><td>{format(b.value)}</td></tr>{/each}
        </tbody>
      </table>
    </div>
  {:else}
    <div class="plot" bind:clientWidth={width}>
      <svg viewBox="0 0 {W} {H}" role="img" aria-label={t("stats.chart_alt", { title })}>
        {#each grid as g (g)}
          <line class="grid" x1={PAD.left} x2={W - PAD.right} y1={y(g)} y2={y(g)} />
          <text class="axis" x={PAD.left - 6} y={y(g) + 4} text-anchor="end">{format(g)}</text>
        {/each}
        <line class="base" x1={PAD.left} x2={W - PAD.right} y1={PAD.top + plotH} y2={PAD.top + plotH} />
        {#each bars as b, i (i)}
          {@const x = PAD.left + i * step + GAP / 2}
          <path class="bar" class:dim={hover !== null && hover !== i} d={barPath(x, b.value)} />
          {#if b.tick}
            <text class="axis" x={x + barW / 2} y={H - 8} text-anchor="middle">{b.tick}</text>
          {/if}
          <!-- Hit target: the full column, wider than the mark. -->
          <rect
            class="hit"
            x={PAD.left + i * step}
            y={PAD.top}
            width={step}
            height={plotH}
            role="presentation"
            onpointerenter={() => (hover = i)}
            onpointerleave={() => (hover = null)}
          />
        {/each}
      </svg>
      {#if hover !== null && bars[hover]}
        {@const b = bars[hover]!}
        <div
          class="tip"
          style="left: {((PAD.left + hover * step + step / 2) / W) * 100}%; top: {(y(b.value) / H) * 100}%"
          aria-hidden="true"
        >
          <span class="muted">{b.label}</span>
          <strong>{format(b.value)}</strong>
        </div>
      {/if}
    </div>
  {/if}
</figure>

<style>
  .chart {
    margin: 0;
  }
  .head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 1rem;
  }
  .head h3 {
    margin: 0;
    font-size: 1.05rem;
  }
  .toggle {
    min-height: 40px;
    padding: 0 0.75rem;
    background: transparent;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    color: var(--text);
    font: inherit;
    font-size: 0.875rem;
    cursor: pointer;
  }
  .plot {
    position: relative;
  }
  svg {
    width: 100%;
    height: auto;
    display: block;
    overflow: visible;
  }
  .grid {
    stroke: var(--border);
    stroke-width: 1;
  }
  .base {
    stroke: var(--text-muted);
    stroke-width: 1;
  }
  .axis {
    fill: var(--text-muted);
    font-size: 11px;
  }
  .bar {
    fill: var(--accent);
    transition: opacity var(--motion);
  }
  .bar.dim {
    opacity: 0.45;
  }
  :global([data-theme="eink"]) .bar.dim {
    opacity: 1;
  }
  .hit {
    fill: transparent;
  }
  .tip {
    position: absolute;
    transform: translate(-50%, calc(-100% - 8px));
    display: flex;
    flex-direction: column;
    align-items: center;
    padding: 0.35rem 0.6rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    box-shadow: var(--shadow);
    font-size: 0.85rem;
    white-space: nowrap;
    pointer-events: none;
  }
  .table-wrap {
    max-height: 20rem;
    overflow: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
  }
  th,
  td {
    text-align: start;
    padding: 0.35rem 0.5rem;
    border-bottom: 1px solid var(--border);
  }
  td:last-child,
  th:last-child {
    text-align: end;
    font-variant-numeric: tabular-nums;
  }
</style>
