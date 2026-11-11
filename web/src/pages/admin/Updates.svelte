<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Server updates (brief §6.21, ADR-0049): the version that runs, the newest
  // release with its notes, and "Update now" when the updater container runs
  // (otherwise the two commands that do the same by hand).
  import { api, type Schemas } from "../../api/client";
  import AdminLayout from "../../components/AdminLayout.svelte";
  import Alert from "../../components/Alert.svelte";
  import Button from "../../components/Button.svelte";
  import Dialog from "../../components/Dialog.svelte";
  import { dateTime } from "../../lib/format";
  import { t, tError } from "../../lib/i18n";

  type Status = Schemas["UpdateStatus"];
  let status = $state<Status | null>(null);
  let error = $state("");
  let checking = $state(false);
  let confirming = $state(false);
  let starting = $state(false);

  async function load() {
    const { data, error: err } = await api.GET("/admin/update");
    if (data) {
      status = data;
      error = "";
    } else if (err) error = tError(err.code, err.message);
  }
  $effect(() => {
    void load();
  });

  // While an update runs the server restarts: keep asking until it's back.
  const busy = $derived(status?.last_update?.status === "requested" || status?.last_update?.status === "running");
  $effect(() => {
    if (!busy) return;
    const id = setInterval(() => void load().catch(() => undefined), 3000);
    return () => clearInterval(id);
  });

  async function check() {
    checking = true;
    const { data, error: err } = await api.POST("/admin/update/check");
    checking = false;
    if (data) status = data;
    else error = tError(err?.code, err?.message);
  }

  async function start() {
    const v = status?.latest?.version;
    if (!v) return;
    starting = true;
    const { data, error: err } = await api.POST("/admin/update", { body: { version: v } });
    starting = false;
    confirming = false;
    if (data) status = data;
    else error = err?.code === "backup_failed" ? t("admin.updates.backup_failed") : tError(err?.code, err?.message);
  }

  const last = $derived(status?.last_update);
  const manual = $derived(
    status?.latest
      ? [
          "# in the folder with docker-compose.yml and .env",
          `sed -i 's/^SIMPLEAPB_VERSION=.*/SIMPLEAPB_VERSION=${status.latest.version}/' .env`,
          "docker compose pull simpleapb && docker compose up -d simpleapb",
        ].join("\n")
      : "",
  );
</script>

<AdminLayout title={t("admin.nav.updates")}>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  {#if !status}
    <p class="muted" role="status">{t("common.loading")}</p>
  {:else}
    <section class="card stack" aria-labelledby="u-now">
      <h2 id="u-now">{t("admin.updates.this_server")}</h2>
      <p>{t("admin.updates.current", { version: status.current_version })}</p>
      {#if status.current_version === "dev"}
        <p class="muted">{t("admin.updates.dev")}</p>
      {/if}
      {#if last}
        {#if last.status === "requested" || last.status === "running"}
          <Alert>{t("admin.updates.running", { version: last.to_version })}</Alert>
        {:else if last.status === "succeeded"}
          <Alert kind="ok">{t("admin.updates.succeeded", { version: last.to_version, date: dateTime(last.at) })}</Alert>
        {:else if last.status === "rolled_back"}
          <Alert kind="error">
            {t("admin.updates.rolled_back", { version: last.to_version, from: last.from_version ?? "" })}
            {#if last.message}<pre class="log">{last.message}</pre>{/if}
          </Alert>
        {:else}
          <Alert kind="error">
            {t("admin.updates.failed", { version: last.to_version })}
            {#if last.message}<pre class="log">{last.message}</pre>{/if}
          </Alert>
        {/if}
      {/if}
    </section>

    <section class="card stack" aria-labelledby="u-latest">
      <h2 id="u-latest">{t("admin.updates.latest_title")}</h2>
      {#if status.check_error}<Alert kind="error">{t("admin.updates.check_failed")}</Alert>{/if}
      {#if status.latest}
        <p>
          {status.update_available
            ? t("admin.updates.available", { version: status.latest.version })
            : t("admin.updates.up_to_date", { version: status.latest.version })}
          <a href={status.latest.url} target="_blank" rel="noopener noreferrer">{t("admin.updates.release_page")}</a>
        </p>
        {#if status.update_available && status.latest.notes}
          <details>
            <summary>{t("admin.updates.notes")}</summary>
            <pre class="notes">{status.latest.notes}</pre>
          </details>
        {/if}
      {:else if status.checked_at}
        <p>{t("admin.updates.no_release")}</p>
      {/if}
      {#if status.checked_at}<p class="muted">{t("admin.updates.checked", { date: dateTime(status.checked_at) })}</p>{/if}
      <div class="row">
        <Button variant="secondary" busy={checking} onclick={() => void check()}>{t("admin.updates.check")}</Button>
        {#if status.update_available && status.updater_present}
          <Button disabled={busy} onclick={() => (confirming = true)}>{t("admin.updates.update_now")}</Button>
        {/if}
      </div>
      {#if status.update_available && !status.updater_present}
        <p>{t("admin.updates.manual")}</p>
        <pre class="cmd">{manual}</pre>
        <p class="muted">{t("admin.updates.manual_hint")}</p>
      {/if}
    </section>
  {/if}
</AdminLayout>

<Dialog open={confirming} title={t("admin.updates.confirm_title")} onclose={() => (confirming = false)}>
  <p>{t("admin.updates.confirm_body", { version: status?.latest?.version ?? "" })}</p>
  <div class="row">
    <Button busy={starting} onclick={() => void start()}>{t("admin.updates.update_now")}</Button>
    <Button variant="secondary" onclick={() => (confirming = false)}>{t("common.cancel")}</Button>
  </div>
</Dialog>

<style>
  .notes,
  .log,
  .cmd {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    font-family: ui-monospace, monospace;
    font-size: 0.9em;
    margin: 0.5rem 0 0;
  }
  .cmd {
    background: var(--surface-2);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.75rem;
  }
</style>
