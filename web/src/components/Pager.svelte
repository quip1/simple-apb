<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Explicit Previous/Next page buttons (e-ink paged lists, brief §11).
  import { t } from "../lib/i18n";
  import Button from "./Button.svelte";

  let { page, hasNext, onprev, onnext }: { page: number; hasNext: boolean; onprev: () => void; onnext: () => void } = $props();
</script>

<nav class="row pager" aria-label={t("common.pages")}>
  <Button variant="secondary" disabled={page <= 1} onclick={onprev}>{t("common.previous_page")}</Button>
  <span>{t("common.page_n", { n: page })}</span>
  <Button variant="secondary" disabled={!hasNext} onclick={onnext}>{t("common.next_page")}</Button>
</nav>

<style>
  .pager {
    justify-content: center;
    margin-top: 1.5rem;
  }
</style>
