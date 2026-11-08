<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import type { Snippet } from "svelte";
  import { session } from "../lib/session.svelte";
  import { t } from "../lib/i18n";
  let { title, wide = false, children }: { title: string; wide?: boolean; children: Snippet } = $props();
</script>

<main class="shell" class:wide>
  <p class="brand">{session.instance?.name ?? t("app.name")}</p>
  <section class="card stack">
    <h1>{title}</h1>
    {@render children()}
  </section>
  {#if session.instance?.copyright_contact}
    <footer class="muted">{session.instance.copyright_contact}</footer>
  {/if}
</main>

<style>
  .shell {
    max-width: 28rem;
    margin: 0 auto;
    padding: 2rem 1rem 4rem;
  }
  .shell.wide {
    max-width: 40rem;
  }
  .brand {
    font-weight: 700;
    letter-spacing: 0.02em;
    margin-bottom: 1rem;
  }
  footer {
    margin-top: 1.5rem;
    font-size: 0.9rem;
  }
</style>
