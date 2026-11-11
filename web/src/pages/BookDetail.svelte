<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Book detail (brief §7.4): cover, people, series, description, chapters,
  // bookmarks, ratings and reviews, who is listening; play, mark finished,
  // add to collection, archive, remove, recommend, share (your uploads),
  // report, edit details (editors, brief §6.12).
  import { api, deviceId, putProgress, type Book, type Schemas } from "../api/client";
  import Alert from "../components/Alert.svelte";
  import Bookmarks from "../components/Bookmarks.svelte";
  import Button from "../components/Button.svelte";
  import ChapterList from "../components/ChapterList.svelte";
  import CollectionPicker from "../components/CollectionPicker.svelte";
  import Cover from "../components/Cover.svelte";
  import Dialog from "../components/Dialog.svelte";
  import MetadataDialog from "../components/MetadataDialog.svelte";
  import ProgressBar from "../components/ProgressBar.svelte";
  import RecommendDialog from "../components/RecommendDialog.svelte";
  import ReportDialog from "../components/ReportDialog.svelte";
  import Reviews from "../components/Reviews.svelte";
  import ShareDialog from "../components/ShareDialog.svelte";
  import { DESKTOP, desktop, download, removeDownload } from "../lib/desktop.svelte";
  import { ago, duration, percent, seriesLabel } from "../lib/format";
  import { t, tError } from "../lib/i18n";
  import { live } from "../lib/live.svelte";
  import { chapterIndexAt, openBook, player, play, seekTo, sendProgress } from "../lib/player.svelte";
  import { link, navigate } from "../lib/router.svelte";
  import { session } from "../lib/session.svelte";

  let { id }: { id: string } = $props();

  let book = $state<Book | null>(null);
  let error = $state("");
  let notice = $state("");
  let busy = $state(false);
  let pickerOpen = $state(false);
  let removeOpen = $state(false);
  let recommendOpen = $state(false);
  let shareOpen = $state(false);
  let reportOpen = $state(false);
  let editOpen = $state(false);
  let listeners = $state<Schemas["Listener"][]>([]);

  async function load(bookId: string) {
    const { data, error: err } = await api.GET("/books/{bookId}", { params: { path: { bookId } } });
    if (err || !data) {
      error = tError(err?.code, err?.message);
      book = null;
      return;
    }
    error = "";
    book = data;
    const l = await api.GET("/books/{bookId}/listeners", { params: { path: { bookId } } });
    listeners = l.data ?? [];
  }

  const me = $derived(session.me);
  const canRecommend = $derived(me?.status === "active");
  const canShare = $derived(
    !!book?.uploaded_by_me && (me?.role === "admin" || !!me?.permissions.can_share || !!me?.permissions.can_post_public),
  );

  $effect(() => {
    void live.tick;
    void load(id);
  });

  const isCurrent = $derived(player.book?.id === book?.id);
  const positionMs = $derived(isCurrent ? player.displayMs : (book?.progress?.position_ms ?? 0));
  const finished = $derived(!!book?.progress?.finished_at);
  const pct = $derived(book ? percent(positionMs, book.duration_ms) : 0);
  const started = $derived(positionMs > 0 && !finished);

  async function playFrom(startMs?: number) {
    if (!book) return;
    if (isCurrent) {
      if (startMs !== undefined) await seekTo(startMs, { play: true });
      else play();
    } else {
      await openBook(book, { startMs, play: true });
    }
    navigate("/player");
  }

  async function setFinished(on: boolean) {
    if (!book) return;
    busy = true;
    const pos = on ? book.duration_ms : 0;
    if (isCurrent) {
      await seekTo(pos, { send: false });
      await sendProgress({ finished: on, force: true, positionMs: pos });
    } else {
      const body = {
        position_ms: pos,
        device_id: deviceId(),
        client_ts: new Date().toISOString(),
        speed: book.progress?.speed ?? 1,
        finished: on,
      };
      let res = await putProgress(api, book.id, body);
      // The user explicitly chose: an explicit choice wins a device conflict.
      if (res.kind === "conflict") res = await putProgress(api, book.id, { ...body, resolution: res.conflict.conflict_id });
      if (res.kind === "error") error = tError(res.error?.code, res.error?.message);
    }
    busy = false;
    await load(book.id);
  }

  async function archive(on: boolean) {
    if (!book) return;
    busy = true;
    const params = { params: { path: { bookId: book.id } } };
    const { error: err } = on
      ? await api.POST("/me/library/{bookId}/hide", params)
      : await api.POST("/me/library/{bookId}/restore", params);
    busy = false;
    if (err) error = tError(err.code, err.message);
    else notice = on ? t("book.archived_notice") : t("book.restored_notice");
    await load(book.id);
  }

  async function remove() {
    if (!book) return;
    busy = true;
    const { error: err } = await api.DELETE("/me/library/{bookId}", { params: { path: { bookId: book.id } } });
    busy = false;
    removeOpen = false;
    if (err) error = tError(err.code, err.message);
    else {
      notice = t("book.removed_notice");
      await load(book.id);
    }
  }

  async function jump(ms: number) {
    await playFrom(ms);
  }
</script>

<svelte:head><title>{book?.title ?? t("common.loading")} · {session.instance?.name ?? ""}</title></svelte:head>

{#if error && !book}
  <Alert kind="error">{error}</Alert>
{:else if !book}
  <p class="muted" role="status">{t("common.loading")}</p>
{:else}
  <article class="detail">
    <div class="art"><Cover src={book.cover_url} has={book.has_cover} title={book.title} size={512} eager /></div>
    <div class="main stack">
      <header>
        <h1>{book.title}</h1>
        {#if book.subtitle}<p class="subtitle muted">{book.subtitle}</p>{/if}
      </header>

      <dl class="meta">
        {#if book.authors.length}
          <dt>{t("book.by")}</dt>
          <dd>
            {#each book.authors as a, i (a.id)}{#if i > 0},
              {/if}<a href="/authors/{a.id}" use:link>{a.name}</a>{/each}
          </dd>
        {/if}
        {#if book.narrators.length}
          <dt>{t("book.narrated_by")}</dt>
          <dd>
            {#each book.narrators as n, i (n.id)}{#if i > 0},
              {/if}<a href="/narrators/{n.id}" use:link>{n.name}</a>{/each}
          </dd>
        {/if}
        {#if book.series}
          <dt>{t("book.series")}</dt>
          <dd>
            <a href="/series/{book.series.id}" use:link>{seriesLabel(book.series)}</a>
          </dd>
        {/if}
        <dt>{t("book.length")}</dt>
        <dd>{duration(book.duration_ms)}</dd>
        {#if book.published_year}
          <dt>{t("book.published")}</dt>
          <dd>{book.publisher ? `${book.publisher}, ${book.published_year}` : book.published_year}</dd>
        {/if}
      </dl>

      {#if finished}
        <p class="state">{t("book.finished")}</p>
      {:else if started}
        <div class="state stack">
          <ProgressBar value={pct} label={t("book.progress_label", { pct })} />
          <p class="muted">{t("book.time_left", { pct, left: duration(book.duration_ms - positionMs) })}</p>
        </div>
      {/if}

      {#if book.needs_review}
        <Alert>{t("meta.needs_review_banner")}</Alert>
      {/if}
      {#if notice}<Alert kind="ok">{notice}</Alert>{/if}
      {#if error}<Alert kind="error">{error}</Alert>{/if}

      {#if book.removed_at}
        <Alert>{t("book.removed_banner")}</Alert>
        <div class="row"><Button {busy} onclick={() => archive(false)}>{t("book.restore")}</Button></div>
      {:else}
        <div class="row actions">
          <Button onclick={() => playFrom()}>
            {isCurrent && player.playing ? t("book.open_player") : started ? t("book.resume") : t("book.play")}
          </Button>
          {#if DESKTOP}
            {@const job = desktop.downloads[id]}
            {#if job?.status === "done"}
              <Button variant="secondary" onclick={() => void removeDownload(id)}>{t("desktop.book.remove_download")}</Button>
            {:else if job?.status === "queued" || job?.status === "downloading"}
              <Button variant="secondary" busy>
                {t("desktop.book.downloading", { pct: job.total_bytes ? Math.round((job.done_bytes / job.total_bytes) * 100) : 0 })}
              </Button>
            {:else}
              <Button variant="secondary" onclick={() => void download(id)}>{t("desktop.book.download")}</Button>
            {/if}
          {/if}
          <Button variant="secondary" {busy} onclick={() => setFinished(!finished)}>
            {finished ? t("book.mark_unfinished") : t("book.mark_finished")}
          </Button>
          <Button variant="secondary" onclick={() => (pickerOpen = true)}>{t("book.add_to_collection")}</Button>
          {#if book.hidden}
            <Button variant="secondary" {busy} onclick={() => archive(false)}>{t("book.unarchive")}</Button>
          {:else}
            <Button variant="secondary" {busy} onclick={() => archive(true)}>{t("book.archive")}</Button>
          {/if}
          {#if canRecommend}
            <Button variant="secondary" onclick={() => (recommendOpen = true)}>{t("book.recommend")}</Button>
          {/if}
          {#if book.can_edit}
            <Button variant="secondary" onclick={() => (editOpen = true)}>{t("book.edit")}</Button>
          {/if}
          {#if canShare}
            <Button variant="secondary" onclick={() => (shareOpen = true)}>{t("book.share")}</Button>
          {/if}
          <Button variant="ghost" onclick={() => (removeOpen = true)}>{t("book.remove")}</Button>
          {#if canRecommend}
            <Button variant="ghost" onclick={() => (reportOpen = true)}>{t("report.action")}</Button>
          {/if}
        </div>
      {/if}

      {#if book.description}
        <section aria-labelledby="about">
          <h2 id="about">{t("book.about")}</h2>
          <p class="description">{book.description}</p>
        </section>
      {/if}

      {#if book.tags.length}
        <ul class="tags" aria-label={t("book.tags")}>
          {#each book.tags as tag (tag.id)}
            <li><a href="/library?tag={tag.id}" use:link>{tag.name}</a></li>
          {/each}
        </ul>
      {/if}

      {#if book.chapters.length}
        <section aria-labelledby="chapters">
          <h2 id="chapters">{t("chapters.title", { n: book.chapters.length })}</h2>
          <ChapterList
            chapters={book.chapters}
            current={started || isCurrent ? chapterIndexAt(book, positionMs) : -1}
            onselect={(c) => playFrom(c.start_ms)}
          />
        </section>
      {/if}

      <section aria-labelledby="bookmarks">
        <h2 id="bookmarks">{t("bookmarks.title")}</h2>
        <Bookmarks bookId={book.id} onjump={jump} />
      </section>

      <section aria-labelledby="reviews">
        <h2 id="reviews">{t("reviews.title")}</h2>
        {#key book.id}<Reviews bookId={book.id} />{/key}
      </section>

      {#if listeners.length}
        <section aria-labelledby="listeners">
          <h2 id="listeners">{t("listeners.title")}</h2>
          <ul class="listeners">
            {#each listeners as l (l.user.id)}
              <li>
                <strong>{l.user.display_name}</strong>
                <span class="muted"
                  >{l.state === "finished" ? t("listeners.finished") : t("listeners.listening", { when: ago(l.updated_at) })}</span
                >
              </li>
            {/each}
          </ul>
        </section>
      {/if}
    </div>
  </article>

  <CollectionPicker bookId={book.id} open={pickerOpen} onclose={() => (pickerOpen = false)} />
  <RecommendDialog open={recommendOpen} bookId={book.id} title={book.title} onclose={() => (recommendOpen = false)} />
  <ShareDialog open={shareOpen} kind="book" id={book.id} title={book.title} onclose={() => (shareOpen = false)} />
  {#if book.can_edit}
    <MetadataDialog
      open={editOpen}
      {book}
      onclose={() => (editOpen = false)}
      onsaved={(b) => {
        book = b;
        notice = t("meta.saved");
      }}
    />
  {/if}
  <ReportDialog open={reportOpen} targets={[{ kind: "book", id: book.id, label: book.title }]} onclose={() => (reportOpen = false)} />
  <Dialog open={removeOpen} title={t("book.remove_title")} onclose={() => (removeOpen = false)}>
    <p>{t("book.remove_body")}</p>
    <div class="row">
      <Button variant="danger" {busy} onclick={remove}>{t("book.remove")}</Button>
      <Button variant="secondary" onclick={() => (removeOpen = false)}>{t("common.cancel")}</Button>
    </div>
  </Dialog>
{/if}

<style>
  .detail {
    display: grid;
    grid-template-columns: minmax(12rem, 20rem) 1fr;
    gap: 2rem;
    align-items: start;
  }
  .art {
    position: sticky;
    top: 5rem;
  }
  .subtitle {
    font-size: 1.1rem;
    margin: 0;
  }
  .meta {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.25rem 1rem;
    margin: 0;
  }
  .meta dt {
    color: var(--text-muted);
  }
  .meta dd {
    margin: 0;
  }
  .meta a {
    display: inline-block;
    padding: 0.15rem 0;
  }
  .state p {
    margin: 0;
  }
  .description {
    white-space: pre-line;
    max-width: 65ch;
  }
  .tags {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  .tags a {
    display: inline-flex;
    align-items: center;
    min-height: 40px;
    padding: 0 0.85rem;
    border: 1px solid var(--border);
    border-radius: 999px;
    color: var(--text);
    text-decoration: none;
  }
  section {
    margin-top: 2rem;
  }
  .listeners {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .listeners li {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 0.75rem;
    min-height: 40px;
    align-items: center;
  }
  @media (max-width: 40rem) {
    .detail {
      grid-template-columns: 1fr;
      gap: 1.25rem;
    }
    .art {
      position: static;
      width: min(16rem, 70vw);
      margin: 0 auto;
    }
  }
</style>
