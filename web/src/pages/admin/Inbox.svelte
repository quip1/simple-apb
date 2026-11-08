<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Moderation inbox (brief §6.16): one view with tabs. Metadata "needs
  // review" joins with the metadata pipeline, duplicates with the book tools.
  import { api, type Schemas } from "../../api/client";
  import AdminLayout from "../../components/AdminLayout.svelte";
  import { t } from "../../lib/i18n";
  import { live } from "../../lib/live.svelte";
  import { navigate, route } from "../../lib/router.svelte";
  import Reports from "./inbox/Reports.svelte";
  import ReviewQueue from "./inbox/ReviewQueue.svelte";
  import Signups from "./inbox/Signups.svelte";

  type Tab = "signups" | "review" | "reports";
  const TABS: { id: Tab; key: string; count: keyof Schemas["InboxCounts"] }[] = [
    { id: "signups", key: "admin.inbox.tab_signups", count: "signup_requests" },
    { id: "review", key: "admin.inbox.tab_review", count: "review_queue" },
    { id: "reports", key: "admin.inbox.tab_reports", count: "reports" },
  ];

  const tab = $derived((TABS.find((x) => x.id === route.query.get("tab"))?.id ?? "signups") as Tab);
  let counts = $state<Schemas["InboxCounts"] | null>(null);

  async function loadCounts() {
    const { data } = await api.GET("/admin/dashboard");
    counts = data?.inbox ?? null;
  }
  $effect(() => {
    void live.tick;
    void loadCounts();
  });

  function select(id: Tab) {
    navigate(id === "signups" ? "/admin/inbox" : `/admin/inbox?tab=${id}`, { replace: true });
  }

  // Arrow keys move between tabs (WAI-ARIA tabs pattern).
  function onKey(e: KeyboardEvent) {
    const i = TABS.findIndex((x) => x.id === tab);
    let n = -1;
    if (e.key === "ArrowRight") n = (i + 1) % TABS.length;
    else if (e.key === "ArrowLeft") n = (i - 1 + TABS.length) % TABS.length;
    else if (e.key === "Home") n = 0;
    else if (e.key === "End") n = TABS.length - 1;
    const to = TABS[n];
    if (!to) return;
    e.preventDefault();
    select(to.id);
    document.getElementById(`tab-${to.id}`)?.focus();
  }
</script>

<AdminLayout title={t("admin.nav.inbox")}>
  <div class="tabs" role="tablist" aria-label={t("admin.nav.inbox")} tabindex="-1" onkeydown={onKey}>
    {#each TABS as x (x.id)}
      <button
        id="tab-{x.id}"
        type="button"
        role="tab"
        aria-selected={tab === x.id}
        aria-controls="panel-{x.id}"
        tabindex={tab === x.id ? 0 : -1}
        onclick={() => select(x.id)}
      >
        {t(x.key)}
        {#if counts && counts[x.count] > 0}<span class="count">{counts[x.count]}</span>{/if}
      </button>
    {/each}
  </div>
  <div id="panel-{tab}" role="tabpanel" aria-labelledby="tab-{tab}" class="stack">
    {#if tab === "signups"}
      <Signups onchange={loadCounts} />
    {:else if tab === "review"}
      <ReviewQueue onchange={loadCounts} />
    {:else}
      <Reports onchange={loadCounts} />
    {/if}
  </div>
</AdminLayout>

<style>
  .tabs {
    display: flex;
    gap: 0.25rem;
    overflow-x: auto;
    border-bottom: 1px solid var(--border);
  }
  .tabs button {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    min-height: 48px;
    padding: 0 0.85rem;
    border: 0;
    border-bottom: 3px solid transparent;
    background: transparent;
    color: var(--text-muted);
    font: inherit;
    white-space: nowrap;
    cursor: pointer;
  }
  .tabs button[aria-selected="true"] {
    color: var(--text);
    font-weight: 600;
    border-bottom-color: var(--accent);
  }
  .count {
    min-width: 1.5rem;
    padding: 0 0.4rem;
    border-radius: 999px;
    background: var(--accent);
    color: var(--accent-text);
    font-size: 0.8rem;
    font-weight: 700;
    text-align: center;
  }
</style>
