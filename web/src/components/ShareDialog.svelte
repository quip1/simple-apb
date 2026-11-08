<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Share a book you uploaded (brief §6.5 rule 6) or a collection with a
  // person or one of your groups. Public shares of books are reviewed first.
  import { api, type Schemas } from "../api/client";
  import { ago } from "../lib/format";
  import { t, tError } from "../lib/i18n";
  import { session } from "../lib/session.svelte";
  import Alert from "./Alert.svelte";
  import Button from "./Button.svelte";
  import Dialog from "./Dialog.svelte";
  import PeoplePicker from "./PeoplePicker.svelte";

  type Share = { id: string; target_type: "group" | "user"; target_name: string };
  type Req = Schemas["ShareRequest"];

  let { open, kind, id, title, onclose }: { open: boolean; kind: "book" | "collection"; id: string; title: string; onclose: () => void } =
    $props();

  let shares = $state<Share[]>([]);
  let requests = $state<Req[]>([]);
  let groups = $state<Schemas["GroupRef"][]>([]);
  let mode = $state<"user" | "group">("user");
  let people = $state<Schemas["UserRef"][]>([]);
  let groupId = $state("");
  let error = $state("");
  let notice = $state("");
  let busy = $state(false);

  const perms = $derived(session.me?.permissions);
  const isAdmin = $derived(session.me?.role === "admin");
  // Books: people and your groups need can_share, Public needs can_post_public.
  const canPrivate = $derived(kind === "collection" || isAdmin || !!perms?.can_share);
  const canPublic = $derived(kind === "collection" || isAdmin || !!perms?.can_post_public);
  const choosable = $derived(groups.filter((g) => (g.public ? canPublic : canPrivate)));
  const chosenGroup = $derived(groups.find((g) => g.id === groupId));

  async function load() {
    if (kind === "book") {
      const { data, error: err } = await api.GET("/books/{bookId}/shares", { params: { path: { bookId: id } } });
      if (err) error = tError(err.code, err.message);
      shares = data?.shares ?? [];
      requests = data?.requests ?? [];
    } else {
      const { data, error: err } = await api.GET("/me/collections/{collectionId}/shares", { params: { path: { collectionId: id } } });
      if (err) error = tError(err.code, err.message);
      shares = data ?? [];
      requests = [];
    }
  }

  $effect(() => {
    if (!open) return;
    error = "";
    notice = "";
    people = [];
    groupId = "";
    mode = canPrivate ? "user" : "group";
    void load();
    void api.GET("/me/groups").then(({ data }) => (groups = data ?? []));
  });

  async function add(e: SubmitEvent) {
    e.preventDefault();
    const target = mode === "user" ? people[0]?.id : groupId;
    if (!target) return;
    busy = true;
    error = "";
    notice = "";
    const body = { target_type: mode, target_id: target };
    const res =
      kind === "book"
        ? await api.POST("/books/{bookId}/shares", { params: { path: { bookId: id } }, body })
        : await api.POST("/me/collections/{collectionId}/shares", { params: { path: { collectionId: id } }, body });
    busy = false;
    if (res.error) {
      error = tError(res.error.code, res.error.message);
      return;
    }
    notice = res.data && "status" in res.data && res.data.status === "pending_review" ? t("share.queued") : t("share.done");
    people = [];
    groupId = "";
    await load();
  }

  async function remove(shareId: string) {
    busy = true;
    const res =
      kind === "book"
        ? await api.DELETE("/books/{bookId}/shares/{shareId}", { params: { path: { bookId: id, shareId } } })
        : await api.DELETE("/me/collections/{collectionId}/shares/{shareId}", { params: { path: { collectionId: id, shareId } } });
    busy = false;
    if (res.error) error = tError(res.error.code, res.error.message);
    await load();
  }
</script>

<Dialog {open} title={t("share.title", { title })} {onclose}>
  <div class="stack">
    <section class="stack" aria-labelledby="sh-current">
      <h3 id="sh-current">{t("share.current")}</h3>
      {#if shares.length || requests.some((r) => r.status === "pending")}
        <ul class="list">
          {#each shares as s (s.id)}
            <li>
              <span><strong>{s.target_name}</strong> <span class="muted">{t(`share.kind_${s.target_type}`)}</span></span>
              <Button variant="ghost" {busy} onclick={() => remove(s.id)}>{t("share.stop")}</Button>
            </li>
          {/each}
          {#each requests.filter((r) => r.status === "pending") as r (r.id)}
            <li>
              <span><strong>{r.target_name}</strong> <span class="pill">{t("share.pending")}</span></span>
              <Button variant="ghost" {busy} onclick={() => remove(r.id)}>{t("share.withdraw")}</Button>
            </li>
          {/each}
        </ul>
      {:else}
        <p class="muted">{t(kind === "book" ? "share.none_book" : "share.none_collection")}</p>
      {/if}
      {#each requests.filter((r) => r.status === "rejected").slice(0, 3) as r (r.id)}
        <Alert>{t("share.rejected", { group: r.target_name, when: ago(r.decided_at ?? r.created_at), reason: r.reason ?? "" })}</Alert>
      {/each}
    </section>

    <form class="stack" onsubmit={add}>
      <h3>{t("share.add")}</h3>
      <fieldset class="row">
        <legend class="sr-only">{t("share.with")}</legend>
        {#if canPrivate}
          <label class="radio"
            ><input type="radio" bind:group={mode} value="user" onchange={() => (notice = "")} /> {t("share.kind_user")}</label
          >
        {/if}
        <label class="radio"
          ><input type="radio" bind:group={mode} value="group" onchange={() => (notice = "")} /> {t("share.kind_group")}</label
        >
      </fieldset>
      {#if mode === "user"}
        <PeoplePicker bind:selected={people} max={1} label={t("share.person")} />
      {:else}
        <label class="field">
          <span>{t("share.group")}</span>
          <select bind:value={groupId}>
            <option value="">{t("admin.dist.choose")}</option>
            {#each choosable as g (g.id)}<option value={g.id}>{g.name}</option>{/each}
          </select>
        </label>
        {#if kind === "book" && chosenGroup?.public && !isAdmin}
          <p class="muted">{t("share.public_hint")}</p>
        {/if}
      {/if}
      {#if notice}<Alert kind="ok">{notice}</Alert>{/if}
      {#if error}<Alert kind="error">{error}</Alert>{/if}
      <div class="row">
        <Button type="submit" {busy} disabled={mode === "user" ? !people.length : !groupId}>{t("share.submit")}</Button>
        <Button variant="secondary" onclick={onclose}>{t("common.close")}</Button>
      </div>
    </form>
  </div>
</Dialog>

<style>
  h3 {
    margin: 0;
    font-size: 1rem;
  }
  .list {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .list li {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 0.5rem;
    min-height: 48px;
  }
  .list li + li {
    border-top: 1px solid var(--border);
  }
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
