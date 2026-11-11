<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Book tools (brief §6.13, ADR-0048), for admins and for uploaders on their
  // own books: the chapter editor (preview, detect from silences), merging
  // the files into one m4b, and the cover picker.
  import { api, type Book, type Schemas } from "../api/client";
  import Alert from "../components/Alert.svelte";
  import Button from "../components/Button.svelte";
  import Cover from "../components/Cover.svelte";
  import Field from "../components/Field.svelte";
  import { clock, duration, parseClock } from "../lib/format";
  import { t, tError } from "../lib/i18n";
  import { waitForJob } from "../lib/jobs";
  import { pause as pausePlayer } from "../lib/player.svelte";
  import { link } from "../lib/router.svelte";
  import { session } from "../lib/session.svelte";

  let { id }: { id: string } = $props();

  type Row = { key: string; title: string; start: string };
  type Candidate = Schemas["MetadataCandidate"];

  let book = $state<Book | null>(null);
  let loadError = $state("");
  let rows = $state<Row[]>([]);
  let chapterNotice = $state("");
  let chapterError = $state("");
  let saving = $state(false);
  let detecting = $state(false);
  let noiseDb = $state("-35");
  let minSilence = $state("2");

  function rowsFrom(cs: { title: string; start_ms: number }[]): Row[] {
    return cs.map((c) => ({ key: crypto.randomUUID(), title: c.title, start: clock(c.start_ms) }));
  }

  async function load() {
    const { data, error } = await api.GET("/books/{bookId}", { params: { path: { bookId: id } } });
    if (!data) {
      loadError = tError(error?.code, error?.message);
      return;
    }
    book = data;
    rows = rowsFrom(data.chapters);
  }
  $effect(() => {
    void load();
  });

  // Several files, or one that isn't already an m4b/m4a (audio/mp4).
  const mergeable = $derived(!!book && (book.files.length > 1 || (book.files.length === 1 && book.files[0]!.mime_type !== "audio/mp4")));

  // ── chapters ──

  function invalidRow(r: Row): boolean {
    const ms = parseClock(r.start);
    return !r.title.trim() || ms === null || (!!book && ms >= book.duration_ms);
  }

  function addRow() {
    const last = rows.length ? (parseClock(rows[rows.length - 1]!.start) ?? 0) : 0;
    const next = Math.min((book?.duration_ms ?? 0) - 1000, last + 60_000);
    rows = [
      ...rows,
      { key: crypto.randomUUID(), title: t("tools.chapters.new_title", { n: rows.length + 1 }), start: clock(Math.max(0, next)) },
    ];
  }

  function removeRow(key: string) {
    rows = rows.filter((r) => r.key !== key);
  }

  async function save() {
    chapterError = chapterNotice = "";
    if (!rows.length || rows.some(invalidRow)) {
      chapterError = t("tools.chapters.invalid");
      return;
    }
    saving = true;
    const { data, error } = await api.PUT("/books/{bookId}/chapters", {
      params: { path: { bookId: id } },
      body: { chapters: rows.map((r) => ({ title: r.title.trim(), start_ms: parseClock(r.start)! })) },
    });
    if (!data) {
      saving = false;
      chapterError = error?.code === "invalid_chapters" ? t("tools.chapters.invalid") : tError(error?.code, error?.message);
      return;
    }
    rows = rowsFrom(data.chapters);
    chapterNotice = t("tools.chapters.saved");
    if (data.job_id) {
      chapterNotice = t("tools.chapters.writing");
      const job = await waitForJob(data.job_id);
      chapterNotice = job?.status === "succeeded" ? t("tools.chapters.written") : "";
      if (job?.status !== "succeeded") chapterError = t("tools.chapters.write_failed");
    }
    saving = false;
  }

  async function detect() {
    chapterError = chapterNotice = "";
    const noise = Number(noiseDb);
    const secs = Number(minSilence);
    if (!Number.isFinite(noise) || noise < -80 || noise > -10 || !Number.isFinite(secs) || secs < 0.3 || secs > 10) {
      chapterError = t("tools.detect.invalid");
      return;
    }
    detecting = true;
    const { data, error } = await api.POST("/books/{bookId}/chapters/detect", {
      params: { path: { bookId: id } },
      body: { noise_db: noise, min_silence_ms: Math.round(secs * 1000) },
    });
    if (!data) {
      detecting = false;
      chapterError = tError(error?.code, error?.message);
      return;
    }
    const job = await waitForJob(data.job_id);
    detecting = false;
    const found = (job?.result?.chapters ?? []) as { title: string; start_ms: number }[];
    if (job?.status !== "succeeded") chapterError = t("tools.detect.failed");
    else {
      rows = rowsFrom(found);
      chapterNotice = t("tools.detect.proposal", { n: found.length });
    }
  }

  // Preview: ten seconds from a chapter start, without moving your progress.
  let preview: HTMLAudioElement | null = null;
  let previewTimer: ReturnType<typeof setTimeout> | undefined;
  let previewing = $state<string | null>(null);
  async function playPreview(r: Row) {
    stopPreview();
    const ms = parseClock(r.start);
    if (!book || ms === null) return;
    const { data } = await api.POST("/books/{bookId}/stream-url", { params: { path: { bookId: id } }, body: { profile: "original" } });
    const file = [...book.files].reverse().find((f) => f.offset_ms <= ms) ?? book.files[0];
    const url = data?.files?.find((f) => f.file_id === file?.id)?.url;
    if (!file || !url) return;
    pausePlayer();
    preview = new Audio(new URL(url, location.href).pathname + new URL(url, location.href).search);
    preview.currentTime = (ms - file.offset_ms) / 1000;
    previewing = r.key;
    await preview.play().catch(() => stopPreview());
    previewTimer = setTimeout(stopPreview, 10_000);
  }
  function stopPreview() {
    clearTimeout(previewTimer);
    preview?.pause();
    preview = null;
    previewing = null;
  }
  $effect(() => stopPreview);

  // ── merge ──

  let reencode = $state(false);
  let keepOriginal = $state(false);
  let merging = $state(false);
  let mergeNotice = $state("");
  let mergeError = $state("");
  let mergedBook = $state("");

  async function merge() {
    mergeError = mergeNotice = mergedBook = "";
    merging = true;
    const { data, error } = await api.POST("/books/{bookId}/merge", {
      params: { path: { bookId: id } },
      body: { reencode, keep_original: keepOriginal },
    });
    if (!data) {
      merging = false;
      mergeError = error?.code === "busy" ? t("tools.merge.busy") : tError(error?.code, error?.message);
      return;
    }
    mergeNotice = t("tools.merge.running");
    const job = await waitForJob(data.job_id, undefined, 2000);
    merging = false;
    if (job?.status !== "succeeded") {
      mergeNotice = "";
      mergeError = t("tools.merge.failed");
      return;
    }
    const newId = String(job.result?.book_id ?? "");
    mergeNotice = keepOriginal ? t("tools.merge.done_new") : t("tools.merge.done");
    if (keepOriginal && newId !== id) mergedBook = newId;
    await load();
  }

  // ── cover ──

  let coverNotice = $state("");
  let coverError = $state("");
  let coverBusy = $state(false);
  let coverVersion = $state(0);
  let candidates = $state<Candidate[] | null>(null);
  let fileInput: HTMLInputElement | undefined = $state();

  async function coverDone(error?: { code?: string; message?: string } | null) {
    coverBusy = false;
    if (error) {
      coverError =
        error.code === "no_embedded_cover"
          ? t("tools.cover.no_embedded")
          : error.code === "bad_image" || error.code === "too_large"
            ? t("tools.cover.bad_image")
            : tError(error.code, error.message);
      return;
    }
    coverNotice = t("tools.cover.saved");
    coverVersion++;
    await load();
  }

  async function uploadCover(e: Event) {
    const file = (e.currentTarget as HTMLInputElement).files?.[0];
    if (!file) return;
    coverError = coverNotice = "";
    coverBusy = true;
    const { error } = await api.POST("/books/{bookId}/cover/upload", {
      params: { path: { bookId: id } },
      body: file as unknown as string,
      bodySerializer: (b: unknown) => b as Blob,
      headers: { "Content-Type": file.type || "image/jpeg" },
    });
    await coverDone(error);
  }

  async function embeddedCover() {
    coverError = coverNotice = "";
    coverBusy = true;
    const { error } = await api.POST("/books/{bookId}/cover/embedded", { params: { path: { bookId: id } } });
    await coverDone(error);
  }

  async function searchCovers() {
    coverError = coverNotice = "";
    coverBusy = true;
    const { data, error } = await api.GET("/books/{bookId}/metadata/candidates", { params: { path: { bookId: id } } });
    coverBusy = false;
    if (error) coverError = tError(error.code, error.message);
    candidates = (data ?? []).filter((c) => !!c.cover_url);
  }

  async function useCandidate(url: string) {
    coverError = coverNotice = "";
    coverBusy = true;
    const { error } = await api.PUT("/books/{bookId}/cover", { params: { path: { bookId: id } }, body: { url } });
    await coverDone(error);
  }
</script>

<svelte:head><title>{t("tools.title")} · {session.instance?.name ?? ""}</title></svelte:head>

{#if loadError}
  <Alert kind="error">{loadError}</Alert>
{:else if !book}
  <p class="muted" role="status">{t("common.loading")}</p>
{:else if !book.can_edit}
  <h1>{t("tools.title")}</h1>
  <Alert kind="error">{t("tools.not_allowed")}</Alert>
{:else}
  <div class="stack">
    <p><a href="/books/{book.id}" use:link>← {book.title}</a></p>
    <h1>{t("tools.title")}</h1>

    <section class="card stack" aria-labelledby="t-chapters">
      <h2 id="t-chapters">{t("tools.chapters.title")}</h2>
      <p class="muted">{t("tools.chapters.help", { length: duration(book.duration_ms) })}</p>
      {#if chapterError}<Alert kind="error">{chapterError}</Alert>{/if}
      {#if chapterNotice}<Alert kind="ok">{chapterNotice}</Alert>{/if}
      <ol class="chapters">
        {#each rows as r, i (r.key)}
          <li class="chapter" class:bad={invalidRow(r)}>
            <span class="num" aria-hidden="true">{i + 1}</span>
            <label class="title">
              <span class="sr-only">{t("tools.chapters.name_label", { n: i + 1 })}</span>
              <input type="text" bind:value={r.title} maxlength="200" />
            </label>
            <label class="start">
              <span class="sr-only">{t("tools.chapters.start_label", { n: i + 1 })}</span>
              <input type="text" inputmode="numeric" bind:value={r.start} disabled={i === 0} />
            </label>
            <Button variant="ghost" onclick={() => (previewing === r.key ? stopPreview() : void playPreview(r))}>
              {previewing === r.key ? t("tools.chapters.stop") : t("tools.chapters.preview")}
            </Button>
            {#if rows.length > 1}
              <Button variant="ghost" onclick={() => removeRow(r.key)}>{t("tools.chapters.remove")}</Button>
            {/if}
          </li>
        {/each}
      </ol>
      <div class="row">
        <Button variant="secondary" onclick={addRow}>{t("tools.chapters.add")}</Button>
        <Button busy={saving} onclick={() => void save()}>{t("tools.chapters.save")}</Button>
        <Button variant="ghost" onclick={() => book && (rows = rowsFrom(book.chapters))}>{t("tools.chapters.undo")}</Button>
      </div>

      <h3>{t("tools.detect.title")}</h3>
      <p class="muted">{t("tools.detect.help")}</p>
      <div class="row fields">
        <Field label={t("tools.detect.noise")} hint={t("tools.detect.noise_hint")} bind:value={noiseDb} inputmode="decimal" />
        <Field label={t("tools.detect.min")} hint={t("tools.detect.min_hint")} bind:value={minSilence} inputmode="decimal" />
      </div>
      <div><Button variant="secondary" busy={detecting} onclick={() => void detect()}>{t("tools.detect.run")}</Button></div>
    </section>

    {#if mergeable}
      <section class="card stack" aria-labelledby="t-merge">
        <h2 id="t-merge">{t("tools.merge.title")}</h2>
        <p class="muted">{t("tools.merge.help", { files: book.files.length })}</p>
        {#if mergeError}<Alert kind="error">{mergeError}</Alert>{/if}
        {#if mergeNotice}<Alert kind="ok">{mergeNotice}</Alert>{/if}
        {#if mergedBook}<p><a href="/books/{mergedBook}" use:link>{t("tools.merge.open_new")}</a></p>{/if}
        <label class="check"><input type="checkbox" bind:checked={reencode} /> {t("tools.merge.reencode")}</label>
        <label class="check"><input type="checkbox" bind:checked={keepOriginal} /> {t("tools.merge.keep")}</label>
        <div><Button busy={merging} onclick={() => void merge()}>{t("tools.merge.run")}</Button></div>
      </section>
    {/if}

    <section class="card stack" aria-labelledby="t-cover">
      <h2 id="t-cover">{t("tools.cover.title")}</h2>
      {#if coverError}<Alert kind="error">{coverError}</Alert>{/if}
      {#if coverNotice}<Alert kind="ok">{coverNotice}</Alert>{/if}
      <div class="cover-row">
        <div class="current">
          {#key coverVersion}<Cover src={book.cover_url} has={book.has_cover} title={book.title} size={256} />{/key}
        </div>
        <div class="stack">
          <input class="sr-only" type="file" accept="image/jpeg,image/png" bind:this={fileInput} onchange={uploadCover} tabindex="-1" />
          <Button variant="secondary" busy={coverBusy} onclick={() => fileInput?.click()}>{t("tools.cover.upload")}</Button>
          <Button variant="secondary" busy={coverBusy} onclick={() => void embeddedCover()}>{t("tools.cover.embedded")}</Button>
          <Button variant="secondary" busy={coverBusy} onclick={() => void searchCovers()}>{t("tools.cover.search")}</Button>
        </div>
      </div>
      {#if candidates}
        {#if candidates.length}
          <ul class="candidates">
            {#each candidates as c, i (i)}
              <li>
                <button type="button" class="pick" onclick={() => c.cover_url && void useCandidate(c.cover_url)}>
                  <img src={c.cover_url} alt={t("tools.cover.candidate", { title: c.title, source: c.provider })} loading="lazy" />
                </button>
              </li>
            {/each}
          </ul>
        {:else}
          <p class="muted">{t("tools.cover.none_found")}</p>
        {/if}
      {/if}
    </section>
  </div>
{/if}

<style>
  .chapters {
    list-style: none;
    padding: 0;
    margin: 0;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .chapter {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
  }
  .chapter.bad input {
    border-color: var(--danger);
  }
  .num {
    width: 2rem;
    text-align: right;
    color: var(--muted);
  }
  .title {
    flex: 1 1 14rem;
  }
  .title input {
    width: 100%;
  }
  .start input {
    width: 7rem;
    font-variant-numeric: tabular-nums;
  }
  .chapter input {
    min-height: 44px;
    padding: 0 0.6rem;
    border-radius: 8px;
    border: 1px solid var(--border);
    background: var(--surface-2);
    color: var(--text);
    font: inherit;
  }
  .fields {
    gap: 1rem;
    flex-wrap: wrap;
  }
  .check {
    display: flex;
    gap: 0.5rem;
    align-items: center;
    min-height: 44px;
  }
  .cover-row {
    display: flex;
    gap: 1.5rem;
    flex-wrap: wrap;
    align-items: flex-start;
  }
  .current {
    width: 12rem;
  }
  .candidates {
    list-style: none;
    padding: 0;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(7rem, 1fr));
    gap: 0.75rem;
  }
  .pick {
    padding: 0;
    border: 2px solid transparent;
    border-radius: 8px;
    background: none;
    cursor: pointer;
    width: 100%;
  }
  .pick:hover,
  .pick:focus-visible {
    border-color: var(--accent);
  }
  .pick img {
    width: 100%;
    border-radius: 6px;
    display: block;
  }
</style>
