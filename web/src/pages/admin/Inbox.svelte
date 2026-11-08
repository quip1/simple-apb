<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Moderation inbox (brief §6.16). Sign-up requests live here now; the review
  // queue and reports join in the moderation milestone.
  import { api, type Schemas } from "../../api/client";
  import AdminLayout from "../../components/AdminLayout.svelte";
  import Alert from "../../components/Alert.svelte";
  import Button from "../../components/Button.svelte";
  import Dialog from "../../components/Dialog.svelte";
  import Field from "../../components/Field.svelte";
  import { ago } from "../../lib/format";
  import { t, tError } from "../../lib/i18n";
  import { live } from "../../lib/live.svelte";

  type Req = Schemas["SignupRequestItem"];
  type Status = "pending" | "approved" | "denied";

  let status = $state<Status>("pending");
  let items = $state<Req[]>([]);
  let error = $state("");
  let busy = $state<string | null>(null);

  async function load(s: Status) {
    const { data, error: err } = await api.GET("/admin/signup-requests", { params: { query: { status: s } } });
    if (err) error = tError(err.code, err.message);
    else items = data ?? [];
  }
  $effect(() => {
    void live.tick;
    void load(status);
  });

  async function approve(r: Req) {
    busy = r.id;
    const { error: err } = await api.POST("/admin/signup-requests/{requestId}/approve", { params: { path: { requestId: r.id } } });
    busy = null;
    if (err) error = tError(err.code, err.message);
    await load(status);
  }

  let denying = $state<Req | null>(null);
  let reason = $state("");
  async function deny(e: SubmitEvent) {
    e.preventDefault();
    if (!denying) return;
    const r = denying;
    busy = r.id;
    const { error: err } = await api.POST("/admin/signup-requests/{requestId}/deny", {
      params: { path: { requestId: r.id } },
      body: { reason: reason.trim() || undefined },
    });
    busy = null;
    denying = null;
    reason = "";
    if (err) error = tError(err.code, err.message);
    await load(status);
  }
</script>

<AdminLayout title={t("admin.nav.inbox")}>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  <section class="card stack" aria-labelledby="i-signups">
    <div class="row head">
      <h2 id="i-signups">{t("admin.inbox.signups")}</h2>
      <label>
        <span class="sr-only">{t("admin.filter_status")}</span>
        <select bind:value={status}>
          {#each ["pending", "approved", "denied"] as s (s)}<option value={s}>{t(`admin.signup.${s}`)}</option>{/each}
        </select>
      </label>
    </div>
    {#if items.length}
      <ul class="reqs">
        {#each items as r (r.id)}
          <li class="stack">
            <div class="row">
              <strong>{r.username}</strong>
              {#if r.email}<span class="muted">{r.email}</span>{/if}
              <span class="muted">{ago(r.created_at)}</span>
            </div>
            {#if r.message}<p class="msg">{r.message}</p>{/if}
            {#if r.status === "pending"}
              <div class="row">
                <Button busy={busy === r.id} onclick={() => approve(r)}>{t("admin.inbox.approve")}</Button>
                <Button variant="secondary" onclick={() => (denying = r)}>{t("admin.inbox.deny")}</Button>
              </div>
            {:else}
              <span class="pill">{t(`admin.signup.${r.status}`)}</span>
            {/if}
          </li>
        {/each}
      </ul>
    {:else}
      <p class="muted">{t("admin.inbox.empty")}</p>
    {/if}
  </section>
  <section class="card stack">
    <h2>{t("admin.inbox.more_title")}</h2>
    <p class="muted">{t("admin.inbox.more_body")}</p>
  </section>
</AdminLayout>

<Dialog open={!!denying} title={t("admin.inbox.deny_title", { user: denying?.username ?? "" })} onclose={() => (denying = null)}>
  <form class="stack" onsubmit={deny}>
    <Field label={t("admin.reason_optional")} bind:value={reason} multiline rows={3} />
    <div class="row">
      <Button variant="danger" type="submit">{t("admin.inbox.deny")}</Button>
      <Button variant="secondary" onclick={() => (denying = null)}>{t("common.cancel")}</Button>
    </div>
  </form>
</Dialog>

<style>
  .head {
    justify-content: space-between;
  }
  .head h2 {
    margin: 0;
  }
  .reqs {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .reqs li {
    padding: 0.85rem 0;
  }
  .reqs li + li {
    border-top: 1px solid var(--border);
  }
  .msg {
    margin: 0;
    white-space: pre-wrap;
  }
</style>
