<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // User tags (brief §6.7): capability overrides for everyone with the tag
  // (most restrictive tag wins), and "require 2FA".
  import { api, type Schemas } from "../../api/client";
  import AdminLayout from "../../components/AdminLayout.svelte";
  import Alert from "../../components/Alert.svelte";
  import Button from "../../components/Button.svelte";
  import Dialog from "../../components/Dialog.svelte";
  import Field from "../../components/Field.svelte";
  import TriState from "../../components/TriState.svelte";
  import { t, tError } from "../../lib/i18n";

  type Tag = Schemas["UserTag"];
  type Perm = keyof Schemas["PermissionOverridesInput"];
  const PERMS: Perm[] = ["can_upload", "can_edit_metadata", "can_delete_own", "can_share", "can_review", "can_post_public"];

  let tags = $state<Tag[]>([]);
  let error = $state("");

  async function load() {
    const { data, error: err } = await api.GET("/admin/user-tags");
    if (err) error = tError(err.code, err.message);
    else tags = data ?? [];
  }
  $effect(() => {
    void load();
  });

  // Editor (new when editing.id is empty).
  let editing = $state<{
    id: string;
    name: string;
    color: string;
    useColor: boolean;
    requireTotp: boolean;
    overrides: Schemas["PermissionOverridesInput"];
  } | null>(null);
  let formError = $state("");

  function edit(tag?: Tag) {
    formError = "";
    editing = tag
      ? {
          id: tag.id,
          name: tag.name,
          color: tag.color ?? "#5b8def",
          useColor: !!tag.color,
          requireTotp: tag.require_totp,
          overrides: { ...tag.capability_overrides },
        }
      : { id: "", name: "", color: "#5b8def", useColor: false, requireTotp: false, overrides: {} };
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    if (!editing) return;
    const body = {
      name: editing.name.trim(),
      color: editing.useColor ? editing.color : "",
      require_totp: editing.requireTotp,
      capability_overrides: editing.overrides,
    };
    const res = editing.id
      ? await api.PATCH("/admin/user-tags/{tagId}", { params: { path: { tagId: editing.id } }, body })
      : await api.POST("/admin/user-tags", { body: { ...body, color: body.color || undefined } });
    if (res.error) {
      formError = tError(res.error.code, res.error.message);
      return;
    }
    editing = null;
    await load();
  }

  let deleting = $state<Tag | null>(null);
  async function remove() {
    if (!deleting) return;
    await api.DELETE("/admin/user-tags/{tagId}", { params: { path: { tagId: deleting.id } } });
    deleting = null;
    await load();
  }

  function summary(tag: Tag): string {
    const parts = PERMS.filter((p) => tag.capability_overrides[p] !== undefined).map((p) =>
      t(tag.capability_overrides[p] ? "admin.tags.allows" : "admin.tags.denies", { perm: t(`admin.perm.${p}`) }),
    );
    if (tag.require_totp) parts.push(t("admin.tags.requires_totp"));
    return parts.join(" · ") || t("admin.tags.no_effect");
  }
</script>

<AdminLayout title={t("admin.nav.tags")}>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  <p class="muted">{t("admin.tags.help")}</p>
  <div class="row"><Button onclick={() => edit()}>{t("admin.tags.new")}</Button></div>
  <div class="table-scroll">
    <table class="data">
      <thead>
        <tr>
          <th scope="col">{t("admin.col.name")}</th>
          <th scope="col">{t("admin.tags.effect")}</th>
          <th scope="col" class="num">{t("admin.col.users")}</th>
          <th scope="col"><span class="sr-only">{t("admin.col.actions")}</span></th>
        </tr>
      </thead>
      <tbody>
        {#each tags as tag (tag.id)}
          <tr>
            <td>
              {#if tag.color}<span class="swatch" style="background: {tag.color}" aria-hidden="true"></span>{/if}
              {tag.name}
            </td>
            <td>{summary(tag)}</td>
            <td class="num">{tag.members}</td>
            <td class="actions">
              <Button variant="ghost" onclick={() => edit(tag)}>{t("admin.edit")}</Button>
              <Button variant="ghost" onclick={() => (deleting = tag)}>{t("admin.delete")}</Button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  {#if !tags.length}<p class="muted">{t("admin.tags.none")}</p>{/if}
</AdminLayout>

<Dialog open={!!editing} title={editing?.id ? t("admin.tags.edit") : t("admin.tags.new")} onclose={() => (editing = null)}>
  {#if editing}
    <form class="stack" onsubmit={save}>
      <Field label={t("admin.col.name")} bind:value={editing.name} required />
      <div class="row">
        <label class="check"><input type="checkbox" bind:checked={editing.useColor} /> {t("admin.tags.color")}</label>
        {#if editing.useColor}<input type="color" bind:value={editing.color} aria-label={t("admin.tags.color")} />{/if}
      </div>
      <label class="check"><input type="checkbox" bind:checked={editing.requireTotp} /> {t("admin.tags.require_totp")}</label>
      {#each PERMS as p (p)}
        <TriState label={t(`admin.perm.${p}`)} bind:value={editing.overrides[p]} />
      {/each}
      {#if formError}<Alert kind="error">{formError}</Alert>{/if}
      <div class="row">
        <Button type="submit">{t("common.save")}</Button>
        <Button variant="secondary" onclick={() => (editing = null)}>{t("common.cancel")}</Button>
      </div>
    </form>
  {/if}
</Dialog>

<Dialog open={!!deleting} title={t("admin.tags.delete_title", { name: deleting?.name ?? "" })} onclose={() => (deleting = null)}>
  <p>{t("admin.tags.delete_body", { n: deleting?.members ?? 0 })}</p>
  <div class="row">
    <Button variant="danger" onclick={remove}>{t("admin.delete")}</Button>
    <Button variant="secondary" onclick={() => (deleting = null)}>{t("common.cancel")}</Button>
  </div>
</Dialog>

<style>
  .swatch {
    display: inline-block;
    width: 0.8rem;
    height: 0.8rem;
    border-radius: 50%;
    border: 1px solid var(--border);
    margin-inline-end: 0.35rem;
    vertical-align: middle;
  }
  .actions {
    white-space: nowrap;
    text-align: end;
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
</style>
