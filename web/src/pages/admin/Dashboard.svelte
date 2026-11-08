<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Admin dashboard (brief §6.23): live streams vs the bandwidth cap, server
  // health, moderation inbox counts and growth. Refreshes every 10 s
  // (every 30 s in e-ink).
  import { api, type Schemas } from "../../api/client";
  import AdminLayout from "../../components/AdminLayout.svelte";
  import Alert from "../../components/Alert.svelte";
  import BarChart from "../../components/BarChart.svelte";
  import ProgressBar from "../../components/ProgressBar.svelte";
  import { ago, bitrate, bytes, dateTime, hours } from "../../lib/format";
  import { t, tError } from "../../lib/i18n";
  import { link } from "../../lib/router.svelte";
  import { themeState } from "../../lib/theme.svelte";

  let dash = $state<Schemas["AdminDashboard"] | null>(null);
  let growth = $state<Schemas["GrowthPoint"][]>([]);
  let error = $state("");
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";

  async function load() {
    const { data, error: err } = await api.GET("/admin/dashboard");
    if (err || !data) error = tError(err?.code, err?.message);
    else {
      error = "";
      dash = data;
    }
  }
  async function loadGrowth() {
    const { data } = await api.GET("/admin/growth", { params: { query: { months: 12, tz } } });
    growth = data ?? [];
  }

  $effect(() => {
    void load();
    void loadGrowth();
    const id = setInterval(load, themeState.theme === "eink" ? 30_000 : 10_000);
    return () => clearInterval(id);
  });

  function uptime(s: number): string {
    const d = Math.floor(s / 86400);
    const h = Math.floor((s % 86400) / 3600);
    return d > 0 ? t("admin.dash.uptime_days", { d, h }) : t("admin.dash.uptime_hours", { h, m: Math.floor((s % 3600) / 60) });
  }
  const pct = (part: number, whole: number) => (whole > 0 ? Math.min(100, Math.round((part / whole) * 100)) : 0);
  const monthFmt = new Intl.DateTimeFormat(document.documentElement.lang || "en", { month: "short", year: "2-digit" });
  const monthLabel = (m: string) => monthFmt.format(new Date(`${m}-15T00:00:00`));
</script>

<AdminLayout title={t("admin.nav.dashboard")}>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  {#if !dash}
    <p class="muted" role="status">{t("common.loading")}</p>
  {:else}
    {@const h = dash.health}
    <dl class="tiles">
      <div class="tile">
        <dt>{t("admin.dash.inbox")}</dt>
        <dd>
          <a href="/admin/inbox" use:link>{t("admin.dash.signups", { n: dash.inbox.signup_requests })}</a>
          <span class="muted small">{t("admin.dash.reviews_reports", { r: dash.inbox.review_queue, p: dash.inbox.reports })}</span>
        </dd>
      </div>
      <div class="tile">
        <dt>{t("admin.dash.bandwidth")}</dt>
        <dd>
          <span>{bitrate(dash.egress.used_bps)} / {bitrate(dash.egress.cap_bps)}</span>
          <ProgressBar value={pct(dash.egress.used_bps, dash.egress.cap_bps)} label={t("admin.dash.bandwidth")} />
        </dd>
      </div>
      <div class="tile">
        <dt>{t("admin.dash.last_backup")}</dt>
        <dd>
          {#if dash.last_backup?.at}
            <span class:bad={!dash.last_backup.ok}
              >{dash.last_backup.ok ? t("admin.ok") : t("admin.failed")} · {ago(dash.last_backup.at)}</span
            >
          {:else}<span class="muted">{t("admin.never")}</span>{/if}
        </dd>
      </div>
      <div class="tile">
        <dt>{t("admin.dash.last_scrub")}</dt>
        <dd>
          {#if dash.last_scrub?.at}
            <span class:bad={!dash.last_scrub.ok}>{dash.last_scrub.ok ? t("admin.ok") : t("admin.failed")} · {ago(dash.last_scrub.at)}</span
            >
          {:else}<span class="muted">{t("admin.never")}</span>{/if}
        </dd>
      </div>
    </dl>

    <section class="card stack" aria-labelledby="d-live">
      <h2 id="d-live">{t("admin.dash.live", { n: dash.live.length })}</h2>
      {#if dash.live.length}
        <div class="table-scroll">
          <table class="data">
            <thead>
              <tr>
                <th scope="col">{t("admin.col.user")}</th>
                <th scope="col">{t("admin.col.book")}</th>
                <th scope="col">{t("admin.col.kind")}</th>
                <th scope="col">{t("admin.col.priority")}</th>
                <th scope="col" class="num">{t("admin.col.bitrate")}</th>
                <th scope="col" class="num">{t("admin.col.rate")}</th>
                <th scope="col">{t("admin.col.since")}</th>
              </tr>
            </thead>
            <tbody>
              {#each dash.live as s, i (i)}
                <tr>
                  <td><a href="/admin/users/{s.user_id}" use:link>{s.username}</a></td>
                  <td><a href="/admin/books/{s.book_id}" use:link>{s.book_title}</a></td>
                  <td>{t(`admin.kind.${s.kind}`)}</td>
                  <td>{t(`admin.priority.${s.priority}`)}</td>
                  <td class="num">{s.bitrate_kbps ? `${s.bitrate_kbps} kbps` : "—"}</td>
                  <td class="num">{bitrate(s.rate_bps)}</td>
                  <td>{ago(s.since)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else}
        <p class="muted">{t("admin.dash.no_live")}</p>
      {/if}
    </section>

    <section class="card stack" aria-labelledby="d-health">
      <h2 id="d-health">{t("admin.dash.health")}</h2>
      <dl class="facts">
        <div>
          <dt>{t("admin.dash.version")}</dt>
          <dd>{dash.version}</dd>
        </div>
        <div>
          <dt>{t("admin.dash.uptime")}</dt>
          <dd>{uptime(dash.uptime_s)} <span class="muted small">({dateTime(dash.started_at)})</span></dd>
        </div>
        <div>
          <dt>{t("admin.dash.db_size")}</dt>
          <dd>{bytes(h.db_bytes)}</dd>
        </div>
        <div>
          <dt>{t("admin.dash.queue")}</dt>
          <dd>{h.job_queue_depth}</dd>
        </div>
        <div>
          <dt>{t("admin.dash.memory")}</dt>
          <dd>
            {#if h.memory_total_bytes && h.memory_available_bytes !== undefined}
              {t("admin.dash.memory_value", {
                used: bytes(h.memory_total_bytes - h.memory_available_bytes),
                total: bytes(h.memory_total_bytes),
                server: bytes(h.process_memory_bytes),
              })}
            {:else}
              {t("admin.dash.memory_server", { server: bytes(h.process_memory_bytes) })}
            {/if}
          </dd>
        </div>
        <div>
          <dt>{t("admin.dash.load")}</dt>
          <dd>
            {h.load_average !== undefined
              ? t("admin.dash.load_value", { load: h.load_average.toFixed(2), cpus: h.cpus ?? 0 })
              : t("admin.unknown")}
          </dd>
        </div>
        <div>
          <dt>{t("admin.dash.zfs")}</dt>
          <dd>{h.zfs === "unknown" ? t("admin.unknown") : h.zfs}</dd>
        </div>
      </dl>
      <h3>{t("admin.dash.disks")}</h3>
      <ul class="disks">
        {#each h.disks as d (d.name)}
          {@const used = d.total_bytes - d.free_bytes}
          <li>
            <span class="dname">{t(`admin.disk.${d.name}`)}</span>
            <ProgressBar value={pct(used, d.total_bytes)} label={t(`admin.disk.${d.name}`)} />
            <span class="muted small">{t("admin.dash.disk_free", { free: bytes(d.free_bytes), total: bytes(d.total_bytes) })}</span>
          </li>
        {/each}
      </ul>
    </section>

    {#if growth.length}
      <section class="card stack" aria-labelledby="d-growth">
        <h2 id="d-growth">{t("admin.dash.growth")}</h2>
        <BarChart
          title={t("admin.dash.listening_per_month")}
          bars={growth.map((g) => ({ label: monthLabel(g.month), tick: monthLabel(g.month), value: g.listening_ms }))}
          format={hours}
        />
        <div class="table-scroll">
          <table class="data">
            <thead>
              <tr>
                <th scope="col">{t("admin.col.month")}</th>
                <th scope="col" class="num">{t("admin.col.users")}</th>
                <th scope="col" class="num">{t("admin.col.books")}</th>
                <th scope="col" class="num">{t("admin.col.storage")}</th>
              </tr>
            </thead>
            <tbody>
              {#each growth.slice().reverse() as g (g.month)}
                <tr>
                  <td>{monthLabel(g.month)}</td>
                  <td class="num">{g.users}</td>
                  <td class="num">{g.books}</td>
                  <td class="num">{bytes(g.storage_bytes)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </section>
    {/if}
  {/if}
</AdminLayout>

<style>
  .tiles {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
    gap: 1rem;
    margin: 0;
  }
  .tile {
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    padding: 1rem;
  }
  .tile dt {
    color: var(--text-muted);
    font-size: 0.875rem;
  }
  .tile dd {
    margin: 0.35rem 0 0;
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    font-weight: 600;
  }
  .small {
    font-size: 0.85rem;
    font-weight: 400;
  }
  .bad {
    color: var(--danger);
  }
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
  h3 {
    margin: 0.5rem 0 0;
    font-size: 1.05rem;
  }
  .disks {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 0.75rem;
  }
  .disks li {
    display: grid;
    grid-template-columns: 7rem minmax(0, 1fr) auto;
    gap: 0.75rem;
    align-items: center;
  }
  @media (max-width: 36rem) {
    .disks li {
      grid-template-columns: 1fr;
      gap: 0.25rem;
    }
  }
</style>
