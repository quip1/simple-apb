<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Distribute one or more books to a group or a user (brief §6.5 rule 4).
  import { api, type Schemas } from "../api/client";
  import { t, tError } from "../lib/i18n";
  import Alert from "./Alert.svelte";
  import Button from "./Button.svelte";
  import Dialog from "./Dialog.svelte";

  let { bookIds, open, onclose, ondone }: { bookIds: string[]; open: boolean; onclose: () => void; ondone: () => void } = $props();

  let groups = $state<Schemas["Group"][]>([]);
  let users = $state<Schemas["AdminUser"][]>([]);
  let kind = $state<"group" | "user">("group");
  let target = $state("");
  let error = $state("");
  let busy = $state(false);

  $effect(() => {
    if (!open) return;
    error = "";
    target = "";
    void Promise.all([api.GET("/admin/groups"), api.GET("/admin/users", { params: { query: { status: "active" } } })]).then(([g, u]) => {
      groups = g.data ?? [];
      users = u.data ?? [];
    });
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!target) return;
    busy = true;
    error = "";
    let failed = 0;
    for (const id of bookIds) {
      const { error: err } = await api.POST("/admin/distributions", { body: { book_id: id, target_type: kind, target_id: target } });
      if (err && err.code !== "conflict") {
        failed++;
        error = tError(err.code, err.message);
      }
    }
    busy = false;
    if (!failed) {
      ondone();
      onclose();
    }
  }
</script>

<Dialog {open} title={t("admin.dist.title", { n: bookIds.length })} {onclose}>
  <form class="stack" onsubmit={submit}>
    <fieldset class="row">
      <legend class="sr-only">{t("admin.dist.to")}</legend>
      <label class="radio"
        ><input type="radio" bind:group={kind} value="group" onchange={() => (target = "")} /> {t("admin.dist.group")}</label
      >
      <label class="radio"
        ><input type="radio" bind:group={kind} value="user" onchange={() => (target = "")} /> {t("admin.dist.user")}</label
      >
    </fieldset>
    <label class="field">
      <span>{kind === "group" ? t("admin.dist.group") : t("admin.dist.user")}</span>
      <select bind:value={target} required>
        <option value="">{t("admin.dist.choose")}</option>
        {#if kind === "group"}
          {#each groups as g (g.id)}<option value={g.id}>{g.name}</option>{/each}
        {:else}
          {#each users as u (u.id)}<option value={u.id}>{u.username}</option>{/each}
        {/if}
      </select>
    </label>
    {#if error}<Alert kind="error">{error}</Alert>{/if}
    <div class="row">
      <Button type="submit" {busy} disabled={!target}>{t("admin.dist.submit")}</Button>
      <Button variant="secondary" onclick={onclose}>{t("common.cancel")}</Button>
    </div>
  </form>
</Dialog>

<style>
  fieldset {
    border: 0;
    padding: 0;
    margin: 0;
  }
  .radio {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    min-height: 48px;
    cursor: pointer;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
</style>
