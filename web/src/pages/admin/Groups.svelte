<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Groups: distribution targets and bandwidth priority (brief §6.5, §6.10).
  import { api, type Schemas } from "../../api/client";
  import AdminLayout from "../../components/AdminLayout.svelte";
  import Alert from "../../components/Alert.svelte";
  import Button from "../../components/Button.svelte";
  import Dialog from "../../components/Dialog.svelte";
  import Field from "../../components/Field.svelte";
  import { t, tError } from "../../lib/i18n";
  import { link } from "../../lib/router.svelte";

  type Group = Schemas["Group"];
  let groups = $state<Group[]>([]);
  let users = $state<Schemas["AdminUser"][]>([]);
  let error = $state("");

  async function load() {
    const [g, u] = await Promise.all([api.GET("/admin/groups"), api.GET("/admin/users")]);
    if (g.error) error = tError(g.error.code, g.error.message);
    groups = g.data ?? [];
    users = (u.data ?? []).filter((x) => x.status !== "pending");
  }
  $effect(() => {
    void load();
  });

  let newName = $state("");
  let newPriority = $state(false);
  async function create(e: SubmitEvent) {
    e.preventDefault();
    if (!newName.trim()) return;
    const { error: err } = await api.POST("/admin/groups", { body: { name: newName.trim(), high_priority: newPriority } });
    if (err) error = tError(err.code, err.message);
    else {
      newName = "";
      newPriority = false;
    }
    await load();
  }

  async function setPriority(g: Group, on: boolean) {
    const { error: err } = await api.PATCH("/admin/groups/{groupId}", { params: { path: { groupId: g.id } }, body: { high_priority: on } });
    if (err) error = tError(err.code, err.message);
    await load();
  }

  let renaming = $state<Group | null>(null);
  let renameTo = $state("");
  async function rename(e: SubmitEvent) {
    e.preventDefault();
    if (!renaming) return;
    const { error: err } = await api.PATCH("/admin/groups/{groupId}", {
      params: { path: { groupId: renaming.id } },
      body: { name: renameTo.trim() },
    });
    if (err) error = tError(err.code, err.message);
    renaming = null;
    await load();
  }

  let deleting = $state<Group | null>(null);
  async function remove() {
    if (!deleting) return;
    const { error: err } = await api.DELETE("/admin/groups/{groupId}", { params: { path: { groupId: deleting.id } } });
    if (err) error = tError(err.code, err.message);
    deleting = null;
    await load();
  }

  // Members of the expanded group.
  let open = $state<string | null>(null);
  let members = $state<Schemas["UserRef"][]>([]);
  let addUser = $state("");
  async function loadMembers(id: string) {
    const { data } = await api.GET("/admin/groups/{groupId}/members", { params: { path: { groupId: id } } });
    members = data ?? [];
  }
  async function toggle(g: Group) {
    open = open === g.id ? null : g.id;
    if (open) await loadMembers(g.id);
  }
  async function addMember(groupId: string) {
    if (!addUser) return;
    await api.PUT("/admin/groups/{groupId}/members/{userId}", { params: { path: { groupId, userId: addUser } } });
    addUser = "";
    await loadMembers(groupId);
    await load();
  }
  async function removeMember(groupId: string, userId: string) {
    await api.DELETE("/admin/groups/{groupId}/members/{userId}", { params: { path: { groupId, userId } } });
    await loadMembers(groupId);
    await load();
  }
  const nonMembers = $derived(users.filter((u) => !members.some((m) => m.id === u.id)));
</script>

<AdminLayout title={t("admin.nav.groups")}>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  <form class="card row" onsubmit={create}>
    <label class="grow">
      <span class="sr-only">{t("admin.groups.name")}</span>
      <input class="text" bind:value={newName} maxlength="100" placeholder={t("admin.groups.new")} />
    </label>
    <label class="check"><input type="checkbox" bind:checked={newPriority} /> {t("admin.groups.priority")}</label>
    <Button type="submit" disabled={!newName.trim()}>{t("admin.create")}</Button>
  </form>

  <ul class="groups">
    {#each groups as g (g.id)}
      <li class="card stack">
        <div class="row line">
          <strong class="grow">{g.name}</strong>
          {#if g.builtin}<span class="pill">{t("admin.groups.builtin")}</span>{/if}
          <span class="muted">{t("admin.groups.members", { n: g.members })}</span>
          <label class="check">
            <input type="checkbox" checked={g.high_priority} onchange={(e) => setPriority(g, e.currentTarget.checked)} />
            {t("admin.groups.priority")}
          </label>
        </div>
        <div class="row">
          <Button variant="secondary" onclick={() => toggle(g)}
            >{open === g.id ? t("admin.groups.hide_members") : t("admin.groups.show_members")}</Button
          >
          {#if !g.builtin}
            <Button
              variant="ghost"
              onclick={() => {
                renaming = g;
                renameTo = g.name;
              }}>{t("admin.rename")}</Button
            >
            <Button variant="ghost" onclick={() => (deleting = g)}>{t("admin.delete")}</Button>
          {/if}
        </div>
        {#if open === g.id}
          <ul class="members">
            {#each members as m (m.id)}
              <li>
                <a href="/admin/users/{m.id}" use:link>{m.username}</a>
                <Button variant="ghost" onclick={() => removeMember(g.id, m.id)}>{t("admin.remove")}</Button>
              </li>
            {:else}
              <li class="muted">{t("admin.groups.no_members")}</li>
            {/each}
          </ul>
          {#if nonMembers.length}
            <div class="row">
              <label>
                <span class="sr-only">{t("admin.groups.add_member")}</span>
                <select bind:value={addUser}>
                  <option value="">{t("admin.groups.add_member")}</option>
                  {#each nonMembers as u (u.id)}<option value={u.id}>{u.username}</option>{/each}
                </select>
              </label>
              <Button variant="secondary" disabled={!addUser} onclick={() => addMember(g.id)}>{t("admin.add")}</Button>
            </div>
          {/if}
        {/if}
      </li>
    {/each}
  </ul>
</AdminLayout>

<Dialog open={!!renaming} title={t("admin.rename")} onclose={() => (renaming = null)}>
  <form class="stack" onsubmit={rename}>
    <Field label={t("admin.groups.name")} bind:value={renameTo} required />
    <div class="row">
      <Button type="submit">{t("common.save")}</Button>
      <Button variant="secondary" onclick={() => (renaming = null)}>{t("common.cancel")}</Button>
    </div>
  </form>
</Dialog>

<Dialog open={!!deleting} title={t("admin.groups.delete_title", { name: deleting?.name ?? "" })} onclose={() => (deleting = null)}>
  <p>{t("admin.groups.delete_body")}</p>
  <div class="row">
    <Button variant="danger" onclick={remove}>{t("admin.delete")}</Button>
    <Button variant="secondary" onclick={() => (deleting = null)}>{t("common.cancel")}</Button>
  </div>
</Dialog>

<style>
  .grow {
    flex: 1 1 12rem;
  }
  .check {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    min-height: 48px;
    cursor: pointer;
  }
  .check input {
    width: 1.2rem;
    height: 1.2rem;
    accent-color: var(--accent);
  }
  .groups,
  .members {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .groups {
    display: grid;
    gap: 1rem;
  }
  .groups > li {
    padding: 1rem 1.25rem;
  }
  .members li {
    display: flex;
    justify-content: space-between;
    align-items: center;
    border-top: 1px solid var(--border);
  }
</style>
