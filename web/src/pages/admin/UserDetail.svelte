<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // One user (brief §6.16 user actions): warn, restrict, suspend, ban,
  // reactivate — each with a logged reason; role, quota, tags, groups,
  // permission overrides, devices and a password reset link.
  import { api, type Schemas } from "../../api/client";
  import AdminLayout from "../../components/AdminLayout.svelte";
  import Alert from "../../components/Alert.svelte";
  import Button from "../../components/Button.svelte";
  import Dialog from "../../components/Dialog.svelte";
  import Field from "../../components/Field.svelte";
  import TriState from "../../components/TriState.svelte";
  import { ago, bytes, dateTime } from "../../lib/format";
  import { t, tError } from "../../lib/i18n";
  import { session } from "../../lib/session.svelte";

  let { id }: { id: string } = $props();

  type Perm = keyof Schemas["PermissionOverridesInput"];
  const PERMS: Perm[] = ["can_upload", "can_edit_metadata", "can_delete_own", "can_share", "can_review", "can_post_public"];

  let user = $state<Schemas["AdminUserDetail"] | null>(null);
  let allTags = $state<Schemas["UserTag"][]>([]);
  let allGroups = $state<Schemas["Group"][]>([]);
  let sessions = $state<Schemas["Session"][]>([]);
  let error = $state("");
  let notice = $state("");

  let perms = $state<Schemas["PermissionOverridesInput"]>({});
  let role = $state<"admin" | "user">("user");
  let quotaGB = $state("0");
  let roleReason = $state("");

  async function load() {
    const [u, tg, gr, ss] = await Promise.all([
      api.GET("/admin/users/{userId}", { params: { path: { userId: id } } }),
      api.GET("/admin/user-tags"),
      api.GET("/admin/groups"),
      api.GET("/admin/users/{userId}/sessions", { params: { path: { userId: id } } }),
    ]);
    if (u.error || !u.data) {
      error = tError(u.error?.code, u.error?.message);
      return;
    }
    user = u.data;
    allTags = tg.data ?? [];
    allGroups = gr.data ?? [];
    sessions = ss.data ?? [];
    perms = { ...u.data.permissions };
    role = u.data.role;
    quotaGB = String(Math.round((u.data.quota_bytes / 1024 ** 3) * 10) / 10);
  }
  $effect(() => {
    void load();
  });

  const self = $derived(session.me?.id === id);

  // ── moderation actions ──
  type Action = "warn" | "restrict" | "suspend" | "ban" | "reactivate";
  let action = $state<Action | null>(null);
  let reason = $state("");
  let warnText = $state("");
  let until = $state("");
  let busy = $state(false);

  async function moderate(e: SubmitEvent) {
    e.preventDefault();
    if (!action || !reason.trim()) return;
    busy = true;
    const body: Schemas["AdminUserPatch"] = { reason: reason.trim() };
    if (action === "warn") body.notice = warnText.trim();
    if (action === "restrict") body.status = "restricted";
    if (action === "ban") body.status = "banned";
    if (action === "reactivate") body.status = "active";
    if (action === "suspend") {
      body.status = "suspended";
      if (until) body.status_until = new Date(until).toISOString();
    }
    const { error: err } = await api.PATCH("/admin/users/{userId}", { params: { path: { userId: id } }, body });
    busy = false;
    if (err) error = tError(err.code, err.message);
    else notice = t("admin.saved");
    action = null;
    reason = warnText = until = "";
    await load();
  }

  async function saveRoleQuota(e: SubmitEvent) {
    e.preventDefault();
    if (!user || !roleReason.trim()) return;
    const quota = Math.max(0, Math.round(Number(quotaGB) * 1024 ** 3));
    const body: Schemas["AdminUserPatch"] = { reason: roleReason.trim(), quota_bytes: quota };
    if (role !== user.role) body.role = role;
    const { error: err } = await api.PATCH("/admin/users/{userId}", { params: { path: { userId: id } }, body });
    if (err) error = tError(err.code, err.message);
    else {
      notice = t("admin.saved");
      roleReason = "";
    }
    await load();
  }

  async function savePerms() {
    const { error: err } = await api.PUT("/admin/users/{userId}/permissions", { params: { path: { userId: id } }, body: perms });
    if (err) error = tError(err.code, err.message);
    else notice = t("admin.saved");
    await load();
  }

  let addTag = $state("");
  async function tagOn() {
    if (!addTag) return;
    await api.PUT("/admin/users/{userId}/tags/{tagId}", { params: { path: { userId: id, tagId: addTag } } });
    addTag = "";
    await load();
  }
  async function tagOff(tagId: string) {
    await api.DELETE("/admin/users/{userId}/tags/{tagId}", { params: { path: { userId: id, tagId } } });
    await load();
  }

  let addGroup = $state("");
  async function groupOn() {
    if (!addGroup) return;
    await api.PUT("/admin/groups/{groupId}/members/{userId}", { params: { path: { groupId: addGroup, userId: id } } });
    addGroup = "";
    await load();
  }
  async function groupOff(groupId: string) {
    await api.DELETE("/admin/groups/{groupId}/members/{userId}", { params: { path: { groupId, userId: id } } });
    await load();
  }

  async function revoke(sessionId: string) {
    await api.DELETE("/admin/sessions/{sessionId}", { params: { path: { sessionId } } });
    await load();
  }

  let resetLink = $state<Schemas["ResetLink"] | null>(null);
  let copied = $state(false);
  async function makeLink() {
    const { data, error: err } = await api.POST("/admin/users/{userId}/reset-link", { params: { path: { userId: id } } });
    if (err || !data) error = tError(err?.code, err?.message);
    else {
      resetLink = data;
      copied = false;
    }
  }
  async function copyLink() {
    if (!resetLink) return;
    try {
      await navigator.clipboard.writeText(resetLink.url);
      copied = true;
    } catch {
      /* select it by hand */
    }
  }

  const freeTags = $derived(allTags.filter((tg) => !user?.tags.some((x) => x.id === tg.id)));
  const freeGroups = $derived(allGroups.filter((g) => !user?.groups.some((x) => x.id === g.id)));
</script>

<AdminLayout title={user ? user.username : t("common.loading")}>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  {#if notice}<Alert kind="ok">{notice}</Alert>{/if}
  {#if user}
    <section class="card stack" aria-labelledby="u-account">
      <h2 id="u-account">{t("admin.user.account")}</h2>
      <dl class="facts">
        <div>
          <dt>{t("admin.user.display_name")}</dt>
          <dd>{user.display_name}</dd>
        </div>
        <div>
          <dt>{t("settings.email")}</dt>
          <dd>{user.email ?? "—"}</dd>
        </div>
        <div>
          <dt>{t("admin.col.status")}</dt>
          <dd>
            <span class="pill" class:warn={user.status !== "active"}>{t(`admin.status.${user.status}`)}</span>
            {#if user.status_until}<span class="muted"> {t("admin.user.until", { date: dateTime(user.status_until) })}</span>{/if}
            {#if user.status_reason && user.status !== "active"}<div class="muted">{user.status_reason}</div>{/if}
          </dd>
        </div>
        <div>
          <dt>{t("admin.col.totp")}</dt>
          <dd>{user.totp_enabled ? t("admin.yes") : t("admin.no")}</dd>
        </div>
        <div>
          <dt>{t("admin.user.storage")}</dt>
          <dd>{bytes(user.used_bytes)} / {user.quota_bytes ? bytes(user.quota_bytes) : t("admin.unlimited")}</dd>
        </div>
        <div>
          <dt>{t("admin.user.last_login")}</dt>
          <dd>{user.last_login_at ? ago(user.last_login_at) : t("admin.never")}</dd>
        </div>
        <div>
          <dt>{t("admin.col.created")}</dt>
          <dd>{dateTime(user.created_at)}</dd>
        </div>
        {#if user.notice}<div>
            <dt>{t("admin.user.pending_warning")}</dt>
            <dd>{user.notice}</dd>
          </div>{/if}
      </dl>
      {#if user.status === "pending"}
        <p class="muted">{t("admin.user.pending_hint")}</p>
      {:else if !self}
        <div class="row">
          <Button variant="secondary" onclick={() => (action = "warn")}>{t("admin.user.warn")}</Button>
          {#if user.status !== "active"}
            <Button variant="secondary" onclick={() => (action = "reactivate")}>{t("admin.user.reactivate")}</Button>
          {/if}
          {#if user.status !== "restricted"}
            <Button variant="secondary" onclick={() => (action = "restrict")}>{t("admin.user.restrict")}</Button>
          {/if}
          {#if user.status !== "suspended"}
            <Button variant="secondary" onclick={() => (action = "suspend")}>{t("admin.user.suspend")}</Button>
          {/if}
          {#if user.status !== "banned"}
            <Button variant="danger" onclick={() => (action = "ban")}>{t("admin.user.ban")}</Button>
          {/if}
        </div>
      {/if}
    </section>

    {#if !self && user.status !== "pending"}
      <section class="card stack" aria-labelledby="u-role">
        <h2 id="u-role">{t("admin.user.role_quota")}</h2>
        <form class="stack" onsubmit={saveRoleQuota}>
          <label class="field">
            <span>{t("admin.col.role")}</span>
            <select bind:value={role}>
              <option value="user">{t("admin.role.user")}</option>
              <option value="admin">{t("admin.role.admin")}</option>
            </select>
          </label>
          <Field label={t("admin.user.quota_gb")} bind:value={quotaGB} inputmode="decimal" hint={t("admin.user.quota_hint")} />
          <Field label={t("admin.reason")} bind:value={roleReason} required />
          <div class="row"><Button type="submit">{t("common.save")}</Button></div>
        </form>
      </section>
    {/if}

    <section class="card stack" aria-labelledby="u-tags">
      <h2 id="u-tags">{t("admin.nav.tags")}</h2>
      <ul class="chips">
        {#each user.tags as tg (tg.id)}
          <li>
            <span class="pill">{tg.name}</span>
            <Button variant="ghost" onclick={() => tagOff(tg.id)}>{t("admin.remove")}</Button>
          </li>
        {:else}
          <li class="muted">{t("admin.user.no_tags")}</li>
        {/each}
      </ul>
      {#if freeTags.length}
        <div class="row">
          <label>
            <span class="sr-only">{t("admin.user.add_tag")}</span>
            <select bind:value={addTag}>
              <option value="">{t("admin.user.add_tag")}</option>
              {#each freeTags as tg (tg.id)}<option value={tg.id}>{tg.name}</option>{/each}
            </select>
          </label>
          <Button variant="secondary" disabled={!addTag} onclick={tagOn}>{t("admin.add")}</Button>
        </div>
      {/if}
    </section>

    <section class="card stack" aria-labelledby="u-groups">
      <h2 id="u-groups">{t("admin.nav.groups")}</h2>
      <ul class="chips">
        {#each user.groups as g (g.id)}
          <li>
            <span class="pill">{g.name}</span>
            <Button variant="ghost" onclick={() => groupOff(g.id)}>{t("admin.remove")}</Button>
          </li>
        {:else}
          <li class="muted">{t("admin.user.no_groups")}</li>
        {/each}
      </ul>
      {#if freeGroups.length}
        <div class="row">
          <label>
            <span class="sr-only">{t("admin.user.add_group")}</span>
            <select bind:value={addGroup}>
              <option value="">{t("admin.user.add_group")}</option>
              {#each freeGroups as g (g.id)}<option value={g.id}>{g.name}</option>{/each}
            </select>
          </label>
          <Button variant="secondary" disabled={!addGroup} onclick={groupOn}>{t("admin.add")}</Button>
        </div>
      {/if}
    </section>

    <section class="card stack" aria-labelledby="u-perms">
      <h2 id="u-perms">{t("admin.user.permissions")}</h2>
      <p class="muted">{t("admin.user.permissions_help")}</p>
      {#each PERMS as p (p)}
        <TriState label={t(`admin.perm.${p}`)} bind:value={perms[p]} effective={user.effective[p]} />
      {/each}
      <div class="row"><Button onclick={savePerms}>{t("common.save")}</Button></div>
    </section>

    <section class="card stack" aria-labelledby="u-devices">
      <h2 id="u-devices">{t("security.devices")}</h2>
      {#if sessions.length}
        <ul class="devices">
          {#each sessions as s (s.id)}
            <li>
              <span
                ><strong>{s.device_name}</strong>
                <span class="muted">{t("security.device_meta", { platform: s.platform, seen: ago(s.last_seen_at) })}</span></span
              >
              <Button variant="ghost" onclick={() => revoke(s.id)}>{t("security.sign_out_device")}</Button>
            </li>
          {/each}
        </ul>
      {:else}
        <p class="muted">{t("admin.user.no_devices")}</p>
      {/if}
    </section>

    {#if user.status !== "pending" && user.status !== "banned"}
      <section class="card stack" aria-labelledby="u-reset">
        <h2 id="u-reset">{t("admin.user.reset_title")}</h2>
        <p class="muted">{t("admin.user.reset_help")}</p>
        {#if resetLink}
          <input class="text mono" readonly value={resetLink.url} aria-label={t("admin.user.reset_title")} />
          <p class="muted">{t("admin.user.reset_expires", { date: dateTime(resetLink.expires_at) })}</p>
          <div class="row">
            <Button variant="secondary" onclick={copyLink}>{copied ? t("security.codes_copied") : t("admin.copy")}</Button>
          </div>
        {:else}
          <div class="row"><Button variant="secondary" onclick={makeLink}>{t("admin.user.reset_create")}</Button></div>
        {/if}
      </section>
    {/if}
  {/if}
</AdminLayout>

<Dialog open={!!action} title={action ? t(`admin.user.${action}`) : ""} onclose={() => (action = null)}>
  <form class="stack" onsubmit={moderate}>
    {#if action}<p class="muted">{t(`admin.user.${action}_help`)}</p>{/if}
    {#if action === "warn"}
      <Field label={t("admin.user.warning_text")} bind:value={warnText} multiline rows={3} required />
    {/if}
    {#if action === "suspend"}
      <label class="field">
        <span>{t("admin.user.suspend_until")}</span>
        <input class="text" type="datetime-local" bind:value={until} />
      </label>
    {/if}
    <Field label={t("admin.reason")} bind:value={reason} required hint={t("admin.reason_hint")} />
    <div class="row">
      <Button variant={action === "ban" ? "danger" : "primary"} type="submit" {busy}>{t("admin.confirm")}</Button>
      <Button variant="secondary" onclick={() => (action = null)}>{t("common.cancel")}</Button>
    </div>
  </form>
</Dialog>

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
  .field {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    max-width: 20rem;
  }
  .chips,
  .devices {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 1rem;
  }
  .chips li {
    display: flex;
    align-items: center;
    gap: 0.25rem;
  }
  .devices li {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 1rem;
    padding: 0.4rem 0;
  }
  .devices li + li {
    border-top: 1px solid var(--border);
  }
</style>
