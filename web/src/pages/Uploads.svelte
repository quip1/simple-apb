<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Upload books (brief §6.11): resumable uploads of .m4b/.m4a/.mp3 or a .zip of
  // one book's mp3 files, then the server's import status. New books are only
  // in your own library until you share them.
  import { api, type Schemas } from "../api/client";
  import Alert from "../components/Alert.svelte";
  import Button from "../components/Button.svelte";
  import ProgressBar from "../components/ProgressBar.svelte";
  import { ago, bytes } from "../lib/format";
  import { t } from "../lib/i18n";
  import { live } from "../lib/live.svelte";
  import { link } from "../lib/router.svelte";
  import { session } from "../lib/session.svelte";
  import { filesFromDrop, pending } from "../lib/dropbook";
  import { tusUpload, UploadError } from "../lib/upload";

  type Item = Schemas["UploadItem"];
  type Sending = { key: string; name: string; size: number; sent: number; error?: string; ctrl: AbortController };

  let sending = $state<Sending[]>([]);
  let recent = $state<Item[]>([]);
  let input: HTMLInputElement | undefined = $state();
  let dragging = $state(false);

  const canUpload = $derived(!!session.me?.permissions.can_upload);
  const busy = $derived(recent.some((u) => u.status === "receiving" || u.status === "processing"));

  async function loadRecent() {
    const { data } = await api.GET("/me/uploads", { params: { query: { limit: 20 } } });
    recent = data ?? [];
  }
  $effect(() => {
    void live.tick;
    void loadRecent();
  });
  // Poll while the server is importing (a push notification also arrives).
  $effect(() => {
    if (!busy && !sending.length) return;
    const id = setInterval(loadRecent, 3000);
    return () => clearInterval(id);
  });

  function start(files: FileList | File[]) {
    for (const file of Array.from(files)) {
      const s: Sending = { key: crypto.randomUUID(), name: file.name, size: file.size, sent: 0, ctrl: new AbortController() };
      sending = [...sending, s];
      void tusUpload(file, (n) => update(s.key, { sent: n }), s.ctrl.signal)
        .then(() => {
          sending = sending.filter((x) => x.key !== s.key);
          void loadRecent();
        })
        .catch((e: unknown) => {
          const kind = e instanceof UploadError ? e.kind : "failed";
          if (kind === "cancelled") sending = sending.filter((x) => x.key !== s.key);
          else update(s.key, { error: t(`uploads.error_${kind}`) });
        });
    }
  }

  function update(key: string, patch: Partial<Sending>) {
    sending = sending.map((x) => (x.key === key ? { ...x, ...patch } : x));
  }

  // Folders too: a folder of mp3s goes as one zip (lib/dropbook.ts).
  function onDrop(e: DragEvent) {
    e.preventDefault();
    e.stopPropagation();
    dragging = false;
    if (e.dataTransfer) void filesFromDrop(e.dataTransfer).then((files) => files.length && start(files));
  }

  // Files dropped elsewhere in the window (the Windows app) start here.
  $effect(() => {
    if (pending.length && canUpload) start(pending.splice(0));
  });
</script>

<svelte:head><title>{t("uploads.title")} · {session.instance?.name ?? ""}</title></svelte:head>

<div class="stack">
  <h1>{t("uploads.title")}</h1>
  {#if !canUpload}
    <Alert>{t("uploads.not_allowed")}</Alert>
  {:else}
    <div
      class="drop card stack"
      class:dragging
      role="region"
      aria-label={t("uploads.drop_label")}
      ondragover={(e) => {
        e.preventDefault();
        dragging = true;
      }}
      ondragleave={() => (dragging = false)}
      ondrop={onDrop}
    >
      <p>{t("uploads.intro")}</p>
      <input
        bind:this={input}
        class="sr-only"
        type="file"
        multiple
        accept=".m4b,.m4a,.mp3,.zip"
        aria-label={t("uploads.choose")}
        onchange={(e) => {
          if (e.currentTarget.files?.length) start(e.currentTarget.files);
          e.currentTarget.value = "";
        }}
      />
      <div class="row"><Button onclick={() => input?.click()}>{t("uploads.choose")}</Button></div>
      <p class="muted small">{t("uploads.formats")}</p>
    </div>

    {#if sending.length}
      <section class="stack" aria-labelledby="up-sending">
        <h2 id="up-sending">{t("uploads.sending")}</h2>
        <ul class="list">
          {#each sending as s (s.key)}
            <li class="stack">
              <div class="row line">
                <strong class="name">{s.name}</strong>
                <span class="muted">{bytes(s.sent)} / {bytes(s.size)}</span>
              </div>
              {#if s.error}
                <Alert kind="error">{s.error}</Alert>
              {:else}
                <ProgressBar
                  value={s.size ? Math.round((s.sent / s.size) * 100) : 0}
                  label={t("uploads.progress", { name: s.name, pct: s.size ? Math.round((s.sent / s.size) * 100) : 0 })}
                />
                <div><Button variant="ghost" onclick={() => s.ctrl.abort()}>{t("common.cancel")}</Button></div>
              {/if}
            </li>
          {/each}
        </ul>
      </section>
    {/if}
  {/if}

  {#if recent.length}
    <section class="stack" aria-labelledby="up-recent">
      <h2 id="up-recent">{t("uploads.recent")}</h2>
      <ul class="list" aria-live="polite">
        {#each recent as u (u.id)}
          <li class="row line">
            <span class="name">
              {#if u.status === "done" && u.book_id}<a href="/books/{u.book_id}" use:link>{u.filename}</a>{:else}{u.filename}{/if}
            </span>
            <span class="pill">{t(`uploads.status_${u.status}`)}</span>
            <span class="muted">{bytes(u.size_bytes)} · {ago(u.updated_at)}</span>
            {#if u.status === "failed" && u.error}<span class="err">{u.error}</span>{/if}
          </li>
        {/each}
      </ul>
    </section>
  {/if}
</div>

<style>
  .drop {
    border-style: dashed;
  }
  .drop.dragging {
    border-color: var(--accent);
  }
  .drop p {
    margin: 0;
  }
  .small {
    font-size: 0.9rem;
  }
  .list {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .list li {
    padding: 0.75rem 0;
  }
  .list li + li {
    border-top: 1px solid var(--border);
  }
  .line {
    gap: 0.5rem 0.75rem;
    flex-wrap: wrap;
  }
  .name {
    overflow-wrap: anywhere;
    flex: 1 1 12rem;
  }
  .err {
    flex-basis: 100%;
    color: var(--danger);
  }
</style>
