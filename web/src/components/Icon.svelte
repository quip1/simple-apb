<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Small inline icon set (decorative: the button around it carries the label).
  type Name =
    "play" | "pause" | "back" | "forward" | "prev" | "next" | "list" | "grid" | "bookmark" | "moon" | "search" | "refresh" | "close";
  let { name, size = 24 }: { name: Name; size?: number } = $props();

  const paths: Record<Name, string> = {
    play: "M8 5.5v13l11-6.5z",
    pause: "M7 5h4v14H7zM13 5h4v14h-4z",
    back: "M12 5V2L7 6l5 4V7a6 6 0 1 1-6 6H4a8 8 0 1 0 8-8z",
    forward: "M12 5V2l5 4-5 4V7a6 6 0 1 0 6 6h2a8 8 0 1 1-8-8z",
    prev: "M6 5h2v14H6zM9.5 12 19 5v14z",
    next: "M16 5h2v14h-2zM5 5l9.5 7L5 19z",
    list: "M4 6h16v2H4zM4 11h16v2H4zM4 16h16v2H4z",
    grid: "M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z",
    bookmark: "M6 3h12v18l-6-4-6 4z",
    moon: "M14.5 3a8.5 8.5 0 1 0 6.5 13.9A7 7 0 0 1 14.5 3z",
    search: "M10 3a7 7 0 0 1 5.6 11.2l5.1 5.1-1.4 1.4-5.1-5.1A7 7 0 1 1 10 3zm0 2a5 5 0 1 0 0 10 5 5 0 0 0 0-10z",
    refresh: "M12 4a8 8 0 0 1 7.4 5H22l-3.5 4L15 9h2.2A6 6 0 1 0 18 14h2.1A8 8 0 1 1 12 4z",
    close: "M6.4 5 5 6.4 10.6 12 5 17.6 6.4 19l5.6-5.6 5.6 5.6 1.4-1.4-5.6-5.6L19 6.4 17.6 5 12 10.6z",
  };
</script>

<svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
  <path d={paths[name]} fill="currentColor" />
</svg>

<style>
  svg {
    display: block;
    flex: none;
  }
</style>
