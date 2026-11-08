<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Audit log (brief §6.16): filterable, exportable as CSV.
  import { api, type Schemas } from "../../api/client";
  import AdminLayout from "../../components/AdminLayout.svelte";
  import Alert from "../../components/Alert.svelte";
  import Button from "../../components/Button.svelte";
  import { dateTime } from "../../lib/format";
  import { t, tError } from "../../lib/i18n";
  import { link } from "../../lib/router.svelte";

  type Entry = Schemas["AuditEntry"];
  const CATEGORIES = [
    "",
    "user.",
    "session.",
    "book.",
    "upload.",
    "distribution.",
    "group.",
    "user_tag.",
    "settings.",
    "backup.",
    "trash.",
    "audit.",
  ];

  let category = $state("");
  let objectType = $state("");
  let from = $state("");
  let to = $state("");
  let items = $state<Entry[]>([]);
  let next = $state<string | undefined>();
  let error = $state("");

  function query(): Record<string, string> {
    const q: Record<string, string> = {};
    if (category) q.action = category;
    if (objectType) q.object_type = objectType;
    if (from) q.from = new Date(`${from}T00:00:00`).toISOString();
    if (to) q.to = new Date(`${to}T00:00:00`).toISOString();
    return q;
  }

  async function load(append = false) {
    const { data, error: err } = await api.GET("/admin/audit", {
      params: { query: { ...query(), cursor: append ? next : undefined, limit: 100 } },
    });
    if (err || !data) {
      error = tError(err?.code, err?.message);
      return;
    }
    error = "";
    items = append ? [...items, ...data.items] : data.items;
    next = data.next_cursor;
  }
  $effect(() => {
    void [category, objectType, from, to];
    void load();
  });

  const exportHref = $derived(`/api/v1/admin/audit/export?${new URLSearchParams(query()).toString()}`);

  function details(e: Entry): string {
    const d = e.details ?? {};
    return Object.entries(d)
      .map(([k, v]) => `${k}: ${typeof v === "object" ? JSON.stringify(v) : String(v)}`)
      .join(" · ");
  }
  function objectLink(e: Entry): string | null {
    if (!e.object_id) return null;
    if (e.object_type === "user") return `/admin/users/${e.object_id}`;
    if (e.object_type === "book") return `/admin/books/${e.object_id}`;
    return null;
  }
</script>

<AdminLayout title={t("admin.nav.audit")}>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  <form class="row filters" onsubmit={(e) => e.preventDefault()}>
    <label class="field">
      <span>{t("admin.audit.category")}</span>
      <select bind:value={category}>
        {#each CATEGORIES as c (c)}<option value={c}>{c ? c.slice(0, -1) : t("admin.audit.all")}</option>{/each}
      </select>
    </label>
    <label class="field">
      <span>{t("admin.audit.object_type")}</span>
      <select bind:value={objectType}>
        <option value="">{t("admin.audit.all")}</option>
        {#each ["user", "book", "group", "user_tag", "session", "distribution"] as o (o)}<option value={o}>{o}</option>{/each}
      </select>
    </label>
    <label class="field">
      <span>{t("admin.audit.from")}</span>
      <input class="text" type="date" bind:value={from} />
    </label>
    <label class="field">
      <span>{t("admin.audit.to")}</span>
      <input class="text" type="date" bind:value={to} />
    </label>
    <a class="btnlink" href={exportHref} download="audit-log.csv">{t("admin.audit.export")}</a>
  </form>

  <div class="table-scroll">
    <table class="data">
      <thead>
        <tr>
          <th scope="col">{t("admin.audit.when")}</th>
          <th scope="col">{t("admin.audit.who")}</th>
          <th scope="col">{t("admin.audit.action")}</th>
          <th scope="col">{t("admin.audit.object")}</th>
          <th scope="col">{t("admin.audit.details")}</th>
        </tr>
      </thead>
      <tbody>
        {#each items as e (e.id)}
          {@const href = objectLink(e)}
          <tr>
            <td class="nowrap">{dateTime(e.at)}</td>
            <td>{e.actor_name ?? (e.actor_id ? e.actor_id.slice(0, 8) : t("admin.audit.system"))}</td>
            <td class="mono">{e.action}</td>
            <td>
              {#if href}<a {href} use:link>{e.object_type}</a>{:else}{e.object_type ?? ""}{/if}
            </td>
            <td class="details"
              >{details(e)}{#if e.ip}<span class="muted">{` · ${e.ip}`}</span>{/if}</td
            >
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  {#if !items.length}<p class="muted">{t("admin.audit.none")}</p>{/if}
  {#if next}<div class="row"><Button variant="secondary" onclick={() => load(true)}>{t("library.load_more")}</Button></div>{/if}
</AdminLayout>

<style>
  .filters {
    align-items: flex-end;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    font-size: 0.875rem;
    color: var(--text-muted);
  }
  .btnlink {
    min-height: 48px;
    display: inline-flex;
    align-items: center;
    padding: 0 1.25rem;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-2);
    color: var(--text);
    font-weight: 600;
    text-decoration: none;
  }
  .nowrap {
    white-space: nowrap;
  }
  .details {
    font-size: 0.875rem;
    overflow-wrap: anywhere;
    min-width: 14rem;
  }
</style>
