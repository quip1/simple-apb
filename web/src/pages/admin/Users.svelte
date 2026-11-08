<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { api, type Schemas } from "../../api/client";
  import AdminLayout from "../../components/AdminLayout.svelte";
  import Alert from "../../components/Alert.svelte";
  import { bytes, dateTime } from "../../lib/format";
  import { t, tError } from "../../lib/i18n";
  import { live } from "../../lib/live.svelte";
  import { link } from "../../lib/router.svelte";

  const STATUSES = ["", "active", "pending", "restricted", "suspended", "banned"] as const;
  let status = $state<(typeof STATUSES)[number]>("");
  let q = $state("");
  let users = $state<Schemas["AdminUser"][]>([]);
  let error = $state("");

  async function load(s: string) {
    const { data, error: err } = await api.GET("/admin/users", {
      params: { query: s ? { status: s as Schemas["UserStatus"] } : {} },
    });
    if (err) error = tError(err.code, err.message);
    else users = data ?? [];
  }
  $effect(() => {
    void live.tick;
    void load(status);
  });

  const shown = $derived(
    users.filter((u) => {
      const n = q.trim().toLowerCase();
      return (
        !n || u.username.toLowerCase().includes(n) || u.display_name.toLowerCase().includes(n) || (u.email ?? "").toLowerCase().includes(n)
      );
    }),
  );
</script>

<AdminLayout title={t("admin.nav.users")}>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  <div class="row">
    <label class="grow">
      <span class="sr-only">{t("admin.users.search")}</span>
      <input class="text" type="search" bind:value={q} placeholder={t("admin.users.search")} />
    </label>
    <label>
      <span class="sr-only">{t("admin.filter_status")}</span>
      <select bind:value={status}>
        {#each STATUSES as s (s)}<option value={s}>{s ? t(`admin.status.${s}`) : t("admin.status.all")}</option>{/each}
      </select>
    </label>
  </div>
  <div class="table-scroll">
    <table class="data">
      <thead>
        <tr>
          <th scope="col">{t("admin.col.user")}</th>
          <th scope="col">{t("admin.col.role")}</th>
          <th scope="col">{t("admin.col.status")}</th>
          <th scope="col">{t("admin.col.totp")}</th>
          <th scope="col" class="num">{t("admin.col.quota")}</th>
          <th scope="col">{t("admin.col.created")}</th>
        </tr>
      </thead>
      <tbody>
        {#each shown as u (u.id)}
          <tr>
            <td>
              <a href="/admin/users/{u.id}" use:link>{u.username}</a>
              {#if u.display_name !== u.username}<span class="muted"> · {u.display_name}</span>{/if}
            </td>
            <td>{t(`admin.role.${u.role}`)}</td>
            <td><span class="pill" class:warn={u.status !== "active"}>{t(`admin.status.${u.status}`)}</span></td>
            <td>{u.totp_enabled ? t("admin.yes") : t("admin.no")}</td>
            <td class="num">{u.quota_bytes ? bytes(u.quota_bytes) : t("admin.unlimited")}</td>
            <td>{dateTime(u.created_at)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  {#if !shown.length}<p class="muted">{t("admin.users.none")}</p>{/if}
</AdminLayout>

<style>
  .grow {
    flex: 1 1 16rem;
  }
</style>
