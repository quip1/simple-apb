<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Backups & integrity (brief §6.18): nightly DB backups (14 kept), weekly
  // scrub; run either now.
  import { api, type Schemas } from "../../api/client";
  import AdminLayout from "../../components/AdminLayout.svelte";
  import Alert from "../../components/Alert.svelte";
  import Button from "../../components/Button.svelte";
  import { ago, bytes, dateTime } from "../../lib/format";
  import { t, tError } from "../../lib/i18n";

  let status = $state<Schemas["BackupStatus"] | null>(null);
  let error = $state("");
  let notice = $state("");
  let running = $state<"backup" | "scrub" | null>(null);

  async function load() {
    const { data, error: err } = await api.GET("/admin/backups");
    if (err || !data) error = tError(err?.code, err?.message);
    else status = data;
  }
  $effect(() => {
    void load();
  });

  async function waitFor(jobId: string): Promise<boolean> {
    for (let i = 0; i < 300; i++) {
      await new Promise((r) => setTimeout(r, 1000));
      const j = await api.GET("/jobs/{jobId}", { params: { path: { jobId } } });
      if (j.data?.status === "succeeded") return true;
      if (j.data?.status === "failed" || j.data?.status === "cancelled") return false;
    }
    return false;
  }

  async function run(kind: "backup" | "scrub") {
    running = kind;
    notice = "";
    const res = kind === "backup" ? await api.POST("/admin/backups") : await api.POST("/admin/integrity-check");
    if (res.error || !res.data) {
      running = null;
      error = tError(res.error?.code, res.error?.message);
      return;
    }
    const ok = await waitFor(res.data.job_id);
    running = null;
    notice = ok ? t(`admin.backups.${kind}_done`) : t(`admin.backups.${kind}_failed`);
    await load();
  }

  function op(s: Schemas["OpStatus"] | undefined): string {
    if (!s?.at) return t("admin.never");
    return `${s.ok ? t("admin.ok") : t("admin.failed")} · ${ago(s.at)}${s.message ? ` · ${s.message}` : ""}`;
  }
</script>

<AdminLayout title={t("admin.nav.backups")}>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  {#if notice}<Alert kind="ok">{notice}</Alert>{/if}
  {#if status}
    <section class="card stack" aria-labelledby="bk-status">
      <h2 id="bk-status">{t("admin.backups.status")}</h2>
      <dl class="facts">
        <div>
          <dt>{t("admin.dash.last_backup")}</dt>
          <dd class:bad={status.last_backup && !status.last_backup.ok}>{op(status.last_backup)}</dd>
        </div>
        <div>
          <dt>{t("admin.dash.last_scrub")}</dt>
          <dd class:bad={status.last_scrub && !status.last_scrub.ok}>{op(status.last_scrub)}</dd>
        </div>
        <div>
          <dt>{t("admin.backups.unclean")}</dt>
          <dd>{status.last_unclean_shutdown?.at ? dateTime(status.last_unclean_shutdown.at) : t("admin.never")}</dd>
        </div>
      </dl>
      <div class="row">
        <Button busy={running === "backup"} disabled={!!running} onclick={() => run("backup")}>{t("admin.backups.run_backup")}</Button>
        <Button variant="secondary" busy={running === "scrub"} disabled={!!running} onclick={() => run("scrub")}
          >{t("admin.backups.run_scrub")}</Button
        >
      </div>
      <p class="muted">{t("admin.backups.help")}</p>
    </section>

    <section class="card stack" aria-labelledby="bk-files">
      <h2 id="bk-files">{t("admin.backups.files", { n: status.backups.length })}</h2>
      {#if status.backups.length}
        <div class="table-scroll">
          <table class="data">
            <thead>
              <tr>
                <th scope="col">{t("admin.col.name")}</th>
                <th scope="col">{t("admin.col.created")}</th>
                <th scope="col" class="num">{t("admin.col.size")}</th>
              </tr>
            </thead>
            <tbody>
              {#each status.backups as b (b.name)}
                <tr>
                  <td class="mono">{b.name}</td>
                  <td>{dateTime(b.created_at)}</td>
                  <td class="num">{bytes(b.size_bytes)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else}
        <p class="muted">{t("admin.backups.none")}</p>
      {/if}
    </section>
  {:else if !error}
    <p class="muted" role="status">{t("common.loading")}</p>
  {/if}
</AdminLayout>

<style>
  .facts {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr));
    gap: 0.75rem 1.5rem;
    margin: 0;
  }
  .facts dt {
    color: var(--text-muted);
    font-size: 0.875rem;
  }
  .facts dd {
    margin: 0;
  }
  .bad {
    color: var(--danger);
  }
</style>
