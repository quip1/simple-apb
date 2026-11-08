<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts" module>
  import type { Schemas } from "../api/client";

  type Kind = Schemas["ReportCreate"]["object_type"];
  export type ReportTarget = { kind: Kind; id: string; label: string };
</script>

<script lang="ts">
  // Report a book, a review or a person to the admins (brief §6.16). With
  // several targets (a review and its author) the reporter picks one.
  import { api } from "../api/client";
  import { t, tError } from "../lib/i18n";
  import Alert from "./Alert.svelte";
  import Button from "./Button.svelte";
  import Dialog from "./Dialog.svelte";
  import Field from "./Field.svelte";

  type Reason = Schemas["ReportReason"];

  let { open, targets, onclose }: { open: boolean; targets: ReportTarget[]; onclose: () => void } = $props();

  let which = $state(0);
  const target = $derived(targets[which] ?? targets[0]);
  const kind = $derived(target?.kind ?? "book");

  const REASONS: Record<Kind, Reason[]> = {
    book: ["copyright", "inappropriate", "wrong_metadata", "spam", "other"],
    review: ["spam", "inappropriate", "harassment", "other"],
    user: ["harassment", "spam", "inappropriate", "other"],
  };

  let reason = $state<Reason | "">("");
  let details = $state("");
  let error = $state("");
  let busy = $state(false);
  let sent = $state(false);

  $effect(() => {
    if (!open) return;
    which = 0;
    reason = "";
    details = "";
    error = "";
    sent = false;
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!reason || !target) return;
    busy = true;
    const { error: err } = await api.POST("/reports", {
      body: { object_type: target.kind, object_id: target.id, reason, details: details.trim() || undefined },
    });
    busy = false;
    if (err) error = tError(err.code, err.message);
    else sent = true;
  }
</script>

<Dialog {open} title={targets.length > 1 ? t("report.title") : t(`report.title_${kind}`, { name: target?.label ?? "" })} {onclose}>
  {#if sent}
    <div class="stack">
      <Alert kind="ok">{t("report.sent")}</Alert>
      <div class="row"><Button onclick={onclose}>{t("common.close")}</Button></div>
    </div>
  {:else}
    <form class="stack" onsubmit={submit}>
      {#if targets.length > 1}
        <fieldset class="stack">
          <legend>{t("report.what")}</legend>
          {#each targets as tg, i (tg.kind + tg.id)}
            <label class="radio"
              ><input type="radio" bind:group={which} value={i} onchange={() => (reason = "")} />
              {t(`report.title_${tg.kind}`, { name: tg.label })}</label
            >
          {/each}
        </fieldset>
      {/if}
      <fieldset class="stack">
        <legend>{t("report.reason")}</legend>
        {#each REASONS[kind] as r (r)}
          <label class="radio"><input type="radio" bind:group={reason} value={r} required /> {t(`report.reason_${r}`)}</label>
        {/each}
      </fieldset>
      <Field label={t("report.details")} hint={t("report.details_hint")} bind:value={details} multiline rows={3} />
      {#if error}<Alert kind="error">{error}</Alert>{/if}
      <div class="row">
        <Button type="submit" {busy} disabled={!reason}>{t("report.submit")}</Button>
        <Button variant="secondary" onclick={onclose}>{t("common.cancel")}</Button>
      </div>
    </form>
  {/if}
</Dialog>

<style>
  fieldset {
    border: 0;
    padding: 0;
    margin: 0;
    gap: 0;
  }
  legend {
    padding: 0;
    margin-bottom: 0.25rem;
    font-weight: 600;
  }
  .radio {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    min-height: 44px;
    cursor: pointer;
  }
</style>
