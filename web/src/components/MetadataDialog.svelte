<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Edit a book's metadata (brief §6.12). Every field you change is locked so
  // the online providers never overwrite it; locked fields can be unlocked.
  // "Find a match" searches the providers and copies a result into the form.
  import { api, type Book, type Schemas } from "../api/client";
  import { t, tError } from "../lib/i18n";
  import Alert from "./Alert.svelte";
  import Button from "./Button.svelte";
  import Dialog from "./Dialog.svelte";
  import Field from "./Field.svelte";

  type FieldName = Schemas["MetadataField"];
  type Candidate = Schemas["MetadataCandidate"];

  let { open, book, onclose, onsaved }: { open: boolean; book: Book; onclose: () => void; onsaved: (b: Book) => void } = $props();

  // Form values as strings; lists are one name per line.
  type Form = Record<FieldName, string> & { series_index: string };
  const lines = (xs: { name: string }[] | string[]) => xs.map((x) => (typeof x === "string" ? x : x.name)).join("\n");

  function fromBook(b: Book): Form {
    return {
      title: b.title,
      subtitle: b.subtitle ?? "",
      description: b.description ?? "",
      publisher: b.publisher ?? "",
      published_year: b.published_year ? String(b.published_year) : "",
      language: b.language ?? "",
      isbn: b.isbn ?? "",
      asin: b.asin ?? "",
      authors: lines(b.authors),
      narrators: lines(b.narrators),
      series: b.series?.name ?? "",
      series_index: b.series?.index ?? "",
      genres: lines(b.tags.filter((x) => x.kind === "genre")),
    };
  }

  const blank = (): Form => ({
    title: "",
    subtitle: "",
    description: "",
    publisher: "",
    published_year: "",
    language: "",
    isbn: "",
    asin: "",
    authors: "",
    narrators: "",
    series: "",
    series_index: "",
    genres: "",
  });
  // Filled from the book each time the dialog opens.
  let initial = $state<Form>(blank());
  let form = $state<Form>(blank());
  let unlock = $state<FieldName[]>([]);
  let error = $state("");
  let busy = $state(false);
  let coverUrl = $state("");
  let useCover = $state(false);

  // Matching.
  let qTitle = $state("");
  let qAuthor = $state("");
  let candidates = $state<Candidate[] | null>(null);
  let searching = $state(false);
  let searchError = $state("");
  let notice = $state("");

  $effect(() => {
    if (!open) return;
    initial = fromBook(book);
    form = fromBook(book);
    unlock = [];
    error = "";
    notice = "";
    coverUrl = "";
    useCover = false;
    candidates = null;
    searchError = "";
    qTitle = book.title;
    qAuthor = book.authors[0]?.name ?? "";
  });

  const locked = $derived(new Set(book.locked_fields ?? []));
  const changed = (f: FieldName) =>
    f === "series" ? form.series !== initial.series || form.series_index !== initial.series_index : form[f] !== initial[f];
  const list = (s: string) =>
    s
      .split("\n")
      .map((x) => x.trim())
      .filter(Boolean);

  async function save(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = "";
    const body: Schemas["BookMetadataPatch"] = {};
    if (changed("title")) body.title = form.title.trim();
    if (changed("subtitle")) body.subtitle = form.subtitle;
    if (changed("description")) body.description = form.description;
    if (changed("publisher")) body.publisher = form.publisher;
    if (changed("published_year")) body.published_year = Number(form.published_year) || 0;
    if (changed("language")) body.language = form.language.trim();
    if (changed("isbn")) body.isbn = form.isbn.trim();
    if (changed("asin")) body.asin = form.asin.trim();
    if (changed("authors")) body.authors = list(form.authors);
    if (changed("narrators")) body.narrators = list(form.narrators);
    if (changed("genres")) body.genres = list(form.genres);
    if (changed("series")) body.series = { name: form.series.trim(), index: form.series_index.trim() || undefined };
    if (unlock.length) body.unlock = unlock;
    let updated: Book | undefined;
    if (Object.keys(body).length) {
      const { data, error: err } = await api.PATCH("/books/{bookId}", { params: { path: { bookId: book.id } }, body });
      if (err || !data) {
        busy = false;
        error = tError(err?.code, err?.message);
        return;
      }
      updated = data;
    }
    if (useCover && coverUrl) {
      const { error: err } = await api.PUT("/books/{bookId}/cover", { params: { path: { bookId: book.id } }, body: { url: coverUrl } });
      if (err) {
        busy = false;
        error = tError(err.code, err.message);
        return;
      }
      const { data } = await api.GET("/books/{bookId}", { params: { path: { bookId: book.id } } });
      updated = data ?? updated;
    }
    busy = false;
    if (updated) onsaved(updated);
    onclose();
  }

  async function search(e?: SubmitEvent) {
    e?.preventDefault();
    searching = true;
    searchError = "";
    const sameAsBook = qTitle.trim() === book.title && qAuthor.trim() === (book.authors[0]?.name ?? "");
    const { data, error: err } = await api.GET("/books/{bookId}/metadata/candidates", {
      params: { path: { bookId: book.id }, query: sameAsBook ? {} : { title: qTitle.trim(), author: qAuthor.trim() } },
    });
    searching = false;
    if (err) searchError = tError(err.code, err.message);
    candidates = data ?? [];
  }

  // Copy a candidate's values into the form (only fields it has).
  function use(c: Candidate) {
    const set = (f: keyof Form, v: string | undefined) => {
      if (v && v.trim()) form[f] = v.trim();
    };
    set("title", c.title);
    set("subtitle", c.subtitle);
    set("description", c.description);
    set("publisher", c.publisher);
    set("published_year", c.published_year ? String(c.published_year) : undefined);
    set("language", c.language);
    set("isbn", c.isbn);
    set("asin", c.asin);
    if (c.authors.length) form.authors = c.authors.join("\n");
    if (c.narrators.length) form.narrators = c.narrators.join("\n");
    if (c.genres.length) form.genres = c.genres.join("\n");
    if (c.series) {
      form.series = c.series;
      form.series_index = c.series_index ?? "";
    }
    coverUrl = c.cover_url ?? "";
    useCover = !!coverUrl && !book.has_cover;
    notice = t("meta.used", { provider: t(`meta.provider_${c.provider}`) });
  }

  async function refresh() {
    busy = true;
    const { error: err } = await api.POST("/books/{bookId}/metadata/refresh", { params: { path: { bookId: book.id } } });
    busy = false;
    if (err) error = tError(err.code, err.message);
    else notice = t("meta.refresh_queued");
  }

  function toggleUnlock(f: FieldName) {
    unlock = unlock.includes(f) ? unlock.filter((x) => x !== f) : [...unlock, f];
  }

  const FIELDS: { f: FieldName; key: string; multiline?: boolean; rows?: number; hint?: string }[] = [
    { f: "title", key: "meta.title" },
    { f: "subtitle", key: "meta.subtitle" },
    { f: "authors", key: "meta.authors", multiline: true, rows: 2, hint: "meta.one_per_line" },
    { f: "narrators", key: "meta.narrators", multiline: true, rows: 2, hint: "meta.one_per_line" },
    { f: "series", key: "meta.series" },
    { f: "published_year", key: "meta.year" },
    { f: "publisher", key: "meta.publisher" },
    { f: "language", key: "meta.language", hint: "meta.language_hint" },
    { f: "isbn", key: "meta.isbn" },
    { f: "asin", key: "meta.asin" },
    { f: "genres", key: "meta.genres", multiline: true, rows: 2, hint: "meta.one_per_line" },
    { f: "description", key: "meta.description", multiline: true, rows: 5 },
  ];
</script>

<Dialog {open} title={t("meta.title_dialog", { title: book.title })} {onclose}>
  <div class="stack">
    <details class="match" open={!!book.needs_review}>
      <summary>{t("meta.find_match")}</summary>
      <form class="stack" onsubmit={search}>
        <div class="two">
          <Field label={t("meta.title")} bind:value={qTitle} />
          <Field label={t("meta.author")} bind:value={qAuthor} />
        </div>
        <div class="row">
          <Button type="submit" variant="secondary" busy={searching}>{t("meta.search")}</Button>
          <Button variant="ghost" {busy} onclick={refresh}>{t("meta.refresh")}</Button>
        </div>
      </form>
      {#if searchError}<Alert kind="error">{searchError}</Alert>{/if}
      {#if candidates}
        {#if candidates.length}
          <ul class="cands" aria-label={t("meta.results")}>
            {#each candidates as c, i (i)}
              <li>
                <div class="stack info">
                  <strong>{c.title}{c.subtitle ? `: ${c.subtitle}` : ""}</strong>
                  <span class="muted">
                    {[c.authors.join(", "), c.published_year, c.publisher].filter(Boolean).join(" · ")}
                  </span>
                  <span class="muted small">
                    {t(`meta.provider_${c.provider}`)} · {t("meta.score", { pct: Math.round(c.score * 100) })}
                    {#if c.cover_url}· {t("meta.has_cover")}{/if}
                  </span>
                </div>
                <Button variant="secondary" onclick={() => use(c)}>{t("meta.use")}</Button>
              </li>
            {/each}
          </ul>
        {:else}
          <p class="muted" role="status">{t("meta.no_results")}</p>
        {/if}
      {/if}
    </details>

    {#if notice}<Alert kind="ok">{notice}</Alert>{/if}

    <form class="stack" onsubmit={save}>
      <p class="muted">{t("meta.lock_explainer")}</p>
      {#each FIELDS as fd (fd.f)}
        <div class="field">
          {#if fd.f === "series"}
            <div class="two">
              <Field label={t(fd.key)} bind:value={form.series} />
              <Field label={t("meta.series_index")} bind:value={form.series_index} />
            </div>
          {:else}
            <Field
              label={t(fd.key)}
              bind:value={form[fd.f]}
              multiline={fd.multiline}
              rows={fd.rows ?? 6}
              hint={fd.hint ? t(fd.hint) : ""}
              inputmode={fd.f === "published_year" ? "numeric" : undefined}
            />
          {/if}
          {#if locked.has(fd.f)}
            <label class="lock">
              <input type="checkbox" checked={unlock.includes(fd.f)} onchange={() => toggleUnlock(fd.f)} />
              {t("meta.locked_unlock")}
            </label>
          {:else if changed(fd.f)}
            <span class="muted small">{t("meta.will_lock")}</span>
          {/if}
        </div>
      {/each}
      {#if coverUrl}
        <label class="lock"><input type="checkbox" bind:checked={useCover} /> {t("meta.use_cover")}</label>
      {/if}
      {#if error}<Alert kind="error">{error}</Alert>{/if}
      <div class="row">
        <Button type="submit" {busy}>{t("common.save")}</Button>
        <Button variant="secondary" onclick={onclose}>{t("common.cancel")}</Button>
      </div>
    </form>
  </div>
</Dialog>

<style>
  .match {
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 0.5rem 0.85rem 0.85rem;
  }
  .match summary {
    min-height: 44px;
    display: flex;
    align-items: center;
    cursor: pointer;
    font-weight: 600;
  }
  .two {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    gap: 0.75rem;
  }
  .cands {
    list-style: none;
    margin: 0.5rem 0 0;
    padding: 0;
  }
  .cands li {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 0.75rem;
    padding: 0.6rem 0;
  }
  .cands li + li {
    border-top: 1px solid var(--border);
  }
  .info {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .small {
    font-size: 0.9rem;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }
  .lock {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    min-height: 40px;
    cursor: pointer;
  }
  @media (max-width: 30rem) {
    .two {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
