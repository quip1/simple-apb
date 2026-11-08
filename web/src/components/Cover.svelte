<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Book cover with a text fallback when there is no cover (never color-only).
  let {
    src,
    title,
    has = true,
    size = 256,
    eager = false,
  }: { src: string; title: string; has?: boolean; size?: 128 | 256 | 512 | 1024; eager?: boolean } = $props();

  let failed = $state(false);
  const double = $derived(size >= 1024 ? 1024 : size * 2);
  // cover_url may already carry a query (a cache-busting version).
  const join = $derived(src.includes("?") ? "&" : "?");
  const initials = $derived(
    title
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((w) => w[0]?.toUpperCase() ?? "")
      .join(""),
  );
</script>

<div class="cover">
  {#if failed || !has}
    <div class="fallback" aria-hidden="true"><span>{initials}</span></div>
  {:else}
    <img
      src="{src}{join}size={size}"
      srcset="{src}{join}size={size} 1x, {src}{join}size={double} 2x"
      alt=""
      loading={eager ? "eager" : "lazy"}
      decoding="async"
      onerror={() => (failed = true)}
    />
  {/if}
</div>

<style>
  .cover {
    aspect-ratio: 1;
    width: 100%;
    border-radius: var(--radius-sm);
    overflow: hidden;
    background: var(--surface-2);
    border: 1px solid var(--border);
  }
  img {
    width: 100%;
    height: 100%;
    object-fit: cover;
    display: block;
  }
  .fallback {
    width: 100%;
    height: 100%;
    display: grid;
    place-items: center;
    font-size: clamp(1rem, 4vw, 2.5rem);
    font-weight: 700;
    color: var(--text-muted);
  }
</style>
