<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Instance settings (brief §6.23): instance, sign-up, quotas, permission
  // defaults, bandwidth, SMTP, ntfy, metadata providers, privacy/terms,
  // copyright contact.
  // Each section saves on its own; secrets are write-only.
  import { api, type Schemas } from "../../api/client";
  import AdminLayout from "../../components/AdminLayout.svelte";
  import Alert from "../../components/Alert.svelte";
  import Button from "../../components/Button.svelte";
  import Field from "../../components/Field.svelte";
  import { t, tError } from "../../lib/i18n";
  import { loadSession } from "../../lib/session.svelte";

  type Perm = keyof Schemas["EffectivePermissions"];
  const PERMS: Perm[] = ["can_upload", "can_edit_metadata", "can_delete_own", "can_share", "can_review", "can_post_public"];

  let s = $state<Schemas["AdminSettings"] | null>(null);
  let error = $state("");
  let saved = $state("");

  // Form copies (strings for text inputs).
  let name = $state("");
  let baseUrl = $state("");
  let signupOpen = $state(true);
  let copyright = $state("");
  let privacy = $state("");
  let terms = $state("");
  let requireTotpAll = $state(false);
  let egress = $state("80");
  let cacheGB = $state("10");
  let quotaGB = $state("0");
  let defaults = $state<Record<Perm, boolean>>({
    can_upload: true,
    can_edit_metadata: true,
    can_delete_own: true,
    can_share: false,
    can_review: true,
    can_post_public: false,
  });
  let smtpHost = $state("");
  let smtpPort = $state("587");
  let smtpTls = $state<"starttls" | "tls" | "none">("starttls");
  let smtpUser = $state("");
  let smtpFrom = $state("");
  let smtpPassword = $state("");
  let ntfyInternal = $state("");
  let ntfyPublic = $state("");
  let ntfyToken = $state("");
  let metaOL = $state(true);
  let metaGB = $state(true);
  let metaAX = $state(true);
  let metaKey = $state("");
  let metaThreshold = $state("80");
  let testTo = $state("");
  let testResult = $state<{ ok: boolean; message?: string } | null>(null);

  function fill(v: Schemas["AdminSettings"]) {
    s = v;
    name = v.instance.name;
    baseUrl = v.instance.base_url;
    signupOpen = v.instance.signup_open;
    copyright = v.instance.copyright_contact;
    privacy = v.instance.privacy_text;
    terms = v.instance.terms_text;
    requireTotpAll = v.auth.require_totp_all;
    egress = String(v.bandwidth.egress_mbps);
    cacheGB = String(v.bandwidth.hls_cache_gb);
    quotaGB = String(Math.round((v.quotas.default_quota_bytes / 1024 ** 3) * 10) / 10);
    defaults = { ...v.permission_defaults };
    smtpHost = v.smtp.host;
    smtpPort = String(v.smtp.port || 587);
    smtpTls = v.smtp.tls;
    smtpUser = v.smtp.username;
    smtpFrom = v.smtp.from;
    smtpPassword = "";
    ntfyInternal = v.ntfy.internal_url;
    ntfyPublic = v.ntfy.public_url;
    ntfyToken = "";
    metaOL = v.metadata.open_library;
    metaGB = v.metadata.google_books;
    metaAX = v.metadata.audnexus;
    metaKey = "";
    metaThreshold = String(Math.round(v.metadata.threshold * 100));
  }

  async function load() {
    const { data, error: err } = await api.GET("/admin/settings");
    if (err || !data) error = tError(err?.code, err?.message);
    else fill(data);
  }
  $effect(() => {
    void load();
  });

  async function save(section: string, body: Schemas["AdminSettingsPatch"]) {
    error = "";
    saved = "";
    const { data, error: err } = await api.PATCH("/admin/settings", { body });
    if (err || !data) {
      error = tError(err?.code, err?.message);
      return;
    }
    fill(data);
    saved = section;
    if (section === "instance") await loadSession();
  }

  const submit = (fn: () => void) => (e: SubmitEvent) => {
    e.preventDefault();
    fn();
  };

  async function testSmtp() {
    testResult = null;
    const { data } = await api.POST("/admin/settings/smtp/test", { body: { to: testTo.trim() } });
    testResult = data ?? { ok: false };
  }
</script>

<AdminLayout title={t("admin.nav.settings")}>
  {#if error}<Alert kind="error">{error}</Alert>{/if}
  {#if s}
    <section class="card stack" aria-labelledby="st-inst">
      <h2 id="st-inst">{t("admin.settings.instance")}</h2>
      <form
        class="stack"
        onsubmit={submit(() =>
          save("instance", {
            instance: {
              name,
              base_url: baseUrl,
              signup_open: signupOpen,
              copyright_contact: copyright,
              privacy_text: privacy,
              terms_text: terms,
            },
          }),
        )}
      >
        <Field label={t("admin.settings.name")} bind:value={name} required />
        <Field label={t("admin.settings.base_url")} bind:value={baseUrl} hint={t("admin.settings.base_url_hint")} required />
        <label class="check"><input type="checkbox" bind:checked={signupOpen} /> {t("admin.settings.signup_open")}</label>
        <Field label={t("admin.settings.copyright")} bind:value={copyright} hint={t("admin.settings.copyright_hint")} />
        <Field label={t("admin.settings.privacy")} bind:value={privacy} multiline rows={6} />
        <Field label={t("admin.settings.terms")} bind:value={terms} multiline rows={6} />
        {#if saved === "instance"}<Alert kind="ok">{t("settings.saved")}</Alert>{/if}
        <div class="row"><Button type="submit">{t("common.save")}</Button></div>
      </form>
    </section>

    <section class="card stack" aria-labelledby="st-access">
      <h2 id="st-access">{t("admin.settings.access")}</h2>
      <form
        class="stack"
        onsubmit={submit(() =>
          save("access", {
            auth: { require_totp_all: requireTotpAll },
            quotas: { default_quota_bytes: Math.max(0, Math.round(Number(quotaGB) * 1024 ** 3)) },
            permission_defaults: defaults,
          }),
        )}
      >
        <label class="check"><input type="checkbox" bind:checked={requireTotpAll} /> {t("admin.settings.require_totp_all")}</label>
        <Field label={t("admin.settings.default_quota")} bind:value={quotaGB} inputmode="decimal" hint={t("admin.user.quota_hint")} />
        <fieldset class="stack">
          <legend>{t("admin.settings.defaults")}</legend>
          {#each PERMS as p (p)}
            <label class="check"><input type="checkbox" bind:checked={defaults[p]} /> {t(`admin.perm.${p}`)}</label>
          {/each}
        </fieldset>
        {#if saved === "access"}<Alert kind="ok">{t("settings.saved")}</Alert>{/if}
        <div class="row"><Button type="submit">{t("common.save")}</Button></div>
      </form>
    </section>

    <section class="card stack" aria-labelledby="st-bw">
      <h2 id="st-bw">{t("admin.settings.bandwidth")}</h2>
      <form
        class="stack"
        onsubmit={submit(() => save("bandwidth", { bandwidth: { egress_mbps: Number(egress), hls_cache_gb: Number(cacheGB) } }))}
      >
        <Field label={t("admin.settings.egress")} bind:value={egress} inputmode="numeric" hint={t("admin.settings.egress_hint")} required />
        <Field label={t("admin.settings.hls_cache")} bind:value={cacheGB} inputmode="numeric" required />
        {#if saved === "bandwidth"}<Alert kind="ok">{t("settings.saved")}</Alert>{/if}
        <div class="row"><Button type="submit">{t("common.save")}</Button></div>
      </form>
    </section>

    <section class="card stack" aria-labelledby="st-smtp">
      <h2 id="st-smtp">{t("admin.settings.smtp")}</h2>
      <p class="muted">{t("setup.smtp.help")}</p>
      <form
        class="stack"
        onsubmit={submit(() =>
          save("smtp", {
            smtp: {
              host: smtpHost,
              port: Number(smtpPort) || 587,
              tls: smtpTls,
              username: smtpUser,
              from: smtpFrom,
              ...(smtpPassword ? { password: smtpPassword } : {}),
            },
          }),
        )}
      >
        <Field label={t("admin.settings.smtp_host")} bind:value={smtpHost} />
        <Field label={t("admin.settings.smtp_port")} bind:value={smtpPort} inputmode="numeric" />
        <label class="field">
          <span>{t("admin.settings.smtp_tls")}</span>
          <select bind:value={smtpTls}>
            <option value="starttls">STARTTLS</option>
            <option value="tls">TLS</option>
            <option value="none">{t("admin.settings.smtp_none")}</option>
          </select>
        </label>
        <Field label={t("admin.settings.smtp_user")} bind:value={smtpUser} autocomplete="off" />
        <Field
          label={t("admin.settings.smtp_password")}
          type="password"
          bind:value={smtpPassword}
          autocomplete="new-password"
          hint={s.smtp.password_set ? t("admin.settings.secret_set") : t("admin.settings.secret_unset")}
        />
        <Field label={t("admin.settings.smtp_from")} bind:value={smtpFrom} type="email" />
        {#if saved === "smtp"}<Alert kind="ok">{t("settings.saved")}</Alert>{/if}
        <div class="row"><Button type="submit">{t("common.save")}</Button></div>
      </form>
      <div class="row test">
        <label class="grow">
          <span class="sr-only">{t("admin.settings.smtp_test_to")}</span>
          <input class="text" type="email" bind:value={testTo} placeholder={t("admin.settings.smtp_test_to")} />
        </label>
        <Button variant="secondary" disabled={!testTo.trim()} onclick={testSmtp}>{t("admin.settings.smtp_test")}</Button>
      </div>
      {#if testResult}
        <Alert kind={testResult.ok ? "ok" : "error"}
          >{testResult.ok ? t("admin.settings.smtp_test_ok") : (testResult.message ?? t("error.generic"))}</Alert
        >
      {/if}
    </section>

    <section class="card stack" aria-labelledby="st-ntfy">
      <h2 id="st-ntfy">{t("admin.settings.ntfy")}</h2>
      <form
        class="stack"
        onsubmit={submit(() =>
          save("ntfy", { ntfy: { internal_url: ntfyInternal, public_url: ntfyPublic, ...(ntfyToken ? { token: ntfyToken } : {}) } }),
        )}
      >
        <Field label={t("admin.settings.ntfy_internal")} bind:value={ntfyInternal} hint={t("admin.settings.ntfy_internal_hint")} />
        <Field label={t("admin.settings.ntfy_public")} bind:value={ntfyPublic} hint={t("admin.settings.ntfy_public_hint")} />
        <Field
          label={t("admin.settings.ntfy_token")}
          type="password"
          bind:value={ntfyToken}
          autocomplete="new-password"
          hint={s.ntfy.token_set ? t("admin.settings.secret_set") : t("admin.settings.secret_unset")}
        />
        {#if saved === "ntfy"}<Alert kind="ok">{t("settings.saved")}</Alert>{/if}
        <div class="row"><Button type="submit">{t("common.save")}</Button></div>
      </form>
    </section>

    <section class="card stack" aria-labelledby="st-meta">
      <h2 id="st-meta">{t("admin.settings.metadata")}</h2>
      <p class="muted">{t("admin.settings.metadata_intro")}</p>
      <form
        class="stack"
        onsubmit={submit(() =>
          save("metadata", {
            metadata: {
              open_library: metaOL,
              google_books: metaGB,
              audnexus: metaAX,
              threshold: Number(metaThreshold) / 100,
              ...(metaKey ? { google_books_key: metaKey } : {}),
            },
          }),
        )}
      >
        <div><label class="check"><input type="checkbox" bind:checked={metaOL} /> {t("admin.settings.meta_openlibrary")}</label></div>
        <div><label class="check"><input type="checkbox" bind:checked={metaGB} /> {t("admin.settings.meta_googlebooks")}</label></div>
        <Field
          label={t("admin.settings.meta_google_key")}
          type="password"
          bind:value={metaKey}
          autocomplete="new-password"
          hint={s.metadata.google_books_key_set ? t("admin.settings.secret_set") : t("admin.settings.meta_google_key_hint")}
        />
        <div><label class="check"><input type="checkbox" bind:checked={metaAX} /> {t("admin.settings.meta_audnexus")}</label></div>
        <Field
          label={t("admin.settings.meta_threshold")}
          type="number"
          inputmode="numeric"
          bind:value={metaThreshold}
          hint={t("admin.settings.meta_threshold_hint")}
        />
        {#if saved === "metadata"}<Alert kind="ok">{t("settings.saved")}</Alert>{/if}
        <div class="row"><Button type="submit">{t("common.save")}</Button></div>
      </form>
    </section>
  {:else if !error}
    <p class="muted" role="status">{t("common.loading")}</p>
  {/if}
</AdminLayout>

<style>
  .check {
    display: inline-flex;
    align-items: center;
    gap: 0.6rem;
    min-height: 44px;
    cursor: pointer;
  }
  .check input {
    width: 1.2rem;
    height: 1.2rem;
    accent-color: var(--accent);
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    max-width: 20rem;
  }
  fieldset {
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 0.75rem 1rem;
    margin: 0;
  }
  legend {
    padding: 0 0.35rem;
    color: var(--text-muted);
  }
  .grow {
    flex: 1 1 14rem;
  }
  .test {
    border-top: 1px solid var(--border);
    padding-top: 1rem;
  }
</style>
