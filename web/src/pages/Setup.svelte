<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- First-run setup wizard (brief §6.2). Uses the one-time token from the server log. -->
<script lang="ts">
  import QRCode from "qrcode";
  import { api } from "../api/client";
  import Alert from "../components/Alert.svelte";
  import AuthShell from "../components/AuthShell.svelte";
  import Button from "../components/Button.svelte";
  import Field from "../components/Field.svelte";
  import { t, tError } from "../lib/i18n";
  import { loadSession } from "../lib/session.svelte";
  import { navigate } from "../lib/router.svelte";
  import privacyTemplate from "../../../docs/PRIVACY-TEMPLATE.md?raw";

  const steps = ["token", "admin", "database", "library", "url", "smtp", "ntfy", "instance", "bandwidth", "review"] as const;
  type Step = (typeof steps)[number];
  let step = $state<Step>("token");
  const index = $derived(steps.indexOf(step));

  let token = $state("");
  let username = $state("admin");
  let password = $state("");
  let password2 = $state("");
  let displayName = $state("");
  let email = $state("");
  let totpSecret = $state("");
  let totpQR = $state("");
  let totpCode = $state("");
  let dialect = $state<"sqlite" | "postgres">("sqlite");
  let dsn = $state("");
  let libraryPath = $state("/library");
  let baseUrl = $state(location.origin);
  let smtpOn = $state(false);
  let smtp = $state({ host: "", port: "587", tls: "starttls" as "starttls" | "tls" | "none", username: "", password: "", from: "" });
  let smtpTestTo = $state("");
  let ntfyInternal = $state("http://ntfy:80");
  let ntfyPublic = $state("");
  let ntfyToken = $state("");
  let instanceName = $state("Simple APB");
  let privacyText = $state(privacyTemplate);
  let termsText = $state(t("setup.terms.template"));
  let signupOpen = $state(true);
  let egress = $state("80");

  let busy = $state(false);
  let error = $state("");
  let notice = $state("");
  let recoveryCodes = $state<string[]>([]);

  const hdr = () => ({ headers: { "X-Setup-Token": token.trim() } });

  function go(s: Step) {
    error = "";
    notice = "";
    step = s;
    queueMicrotask(() => document.querySelector<HTMLElement>("h2")?.focus());
  }
  const next = () => go(steps[index + 1]!);
  const back = () => go(steps[index - 1]!);

  async function checkToken() {
    busy = true;
    // Any token-protected call proves the token; generating the TOTP secret is harmless.
    const r = await api.POST("/setup/totp", { body: { username: username || "admin" }, ...hdr() });
    if (r.error) {
      busy = false;
      error = tError(r.error.code, r.error.message);
      return;
    }
    totpSecret = r.data.secret;
    totpQR = await QRCode.toDataURL(r.data.otpauth_uri, { margin: 1, width: 220 });
    busy = false; // stay busy until the QR code is ready
    next();
  }

  async function newTotp() {
    const r = await api.POST("/setup/totp", { body: { username }, ...hdr() });
    if (r.data) {
      totpSecret = r.data.secret;
      totpQR = await QRCode.toDataURL(r.data.otpauth_uri, { margin: 1, width: 220 });
    }
  }

  function checkAdmin() {
    if (password.length < 10) return (error = t("error.password_too_short"));
    if (password !== password2) return (error = t("setup.admin.mismatch"));
    if (!/^[0-9]{6}$/.test(totpCode.trim())) return (error = t("setup.admin.code_needed"));
    next();
  }

  async function testDb() {
    busy = true;
    const r = await api.POST("/setup/database/test", { body: { dialect, dsn: dialect === "postgres" ? dsn : undefined }, ...hdr() });
    busy = false;
    if (r.error) return (error = tError(r.error.code, r.error.message));
    if (!r.data.ok) return (error = r.data.message ?? t("error.generic"));
    notice = t("setup.db.ok");
  }

  async function testLibrary() {
    busy = true;
    const r = await api.POST("/setup/library/test", { body: { path: libraryPath }, ...hdr() });
    busy = false;
    if (r.error) return (error = tError(r.error.code, r.error.message));
    if (!r.data.exists || !r.data.writable) return (error = r.data.message ?? t("setup.library.unusable"));
    notice = r.data.empty ? t("setup.library.empty") : t("setup.library.ok");
    return true;
  }

  function smtpBody() {
    return {
      host: smtp.host,
      port: Number(smtp.port),
      tls: smtp.tls,
      username: smtp.username || undefined,
      password: smtp.password || undefined,
      from: smtp.from,
    };
  }

  async function testSmtp() {
    busy = true;
    const r = await api.POST("/setup/smtp/test", { body: { smtp: smtpBody(), to: smtpTestTo }, ...hdr() });
    busy = false;
    if (r.error) return (error = tError(r.error.code, r.error.message));
    if (!r.data.ok) return (error = r.data.message ?? t("error.generic"));
    notice = t("setup.smtp.sent", { to: smtpTestTo });
  }

  async function complete() {
    busy = true;
    error = "";
    const r = await api.POST("/setup/complete", {
      ...hdr(),
      body: {
        admin: {
          username,
          password,
          display_name: displayName || undefined,
          email: email || undefined,
          totp_secret: totpSecret,
          totp_code: totpCode.trim(),
        },
        database: { dialect, dsn: dialect === "postgres" ? dsn : undefined },
        library_path: libraryPath,
        base_url: baseUrl,
        smtp: smtpOn ? smtpBody() : undefined,
        ntfy: { internal_url: ntfyInternal || undefined, public_url: ntfyPublic || undefined, token: ntfyToken || undefined },
        instance_name: instanceName,
        privacy_text: privacyText,
        terms_text: termsText,
        signup_open: signupOpen,
        egress_mbps: Number(egress) || 80,
      },
    });
    busy = false;
    if (r.error) {
      error = tError(r.error.code, r.error.message);
      if (r.error.code === "invalid_totp") step = "admin";
      return;
    }
    recoveryCodes = r.data.recovery_codes;
  }

  async function finish() {
    await loadSession();
    navigate("/login", { replace: true });
  }
</script>

<AuthShell title={t("setup.title")} wide>
  {#if recoveryCodes.length}
    <h2 tabindex="-1">{t("setup.done.title")}</h2>
    <p>{t("setup.done.codes")}</p>
    <ol class="codes mono">
      {#each recoveryCodes as c (c)}<li>{c}</li>{/each}
    </ol>
    <Button onclick={finish}>{t("setup.done.continue")}</Button>
  {:else}
    <p class="muted" aria-live="polite">{t("setup.progress", { step: index + 1, total: steps.length })}</p>

    {#if step === "token"}
      <h2 tabindex="-1">{t("setup.token.title")}</h2>
      <p>{t("setup.token.help")}</p>
      <Field label={t("setup.token.label")} bind:value={token} autocomplete="off" required />
      <div class="row"><Button {busy} disabled={!token.trim()} onclick={checkToken}>{t("common.continue")}</Button></div>
    {:else if step === "admin"}
      <h2 tabindex="-1">{t("setup.admin.title")}</h2>
      <Field label={t("auth.username")} bind:value={username} autocomplete="username" required />
      <Field label={t("setup.admin.display_name")} bind:value={displayName} autocomplete="name" />
      <Field label={t("auth.email")} type="email" bind:value={email} autocomplete="email" />
      <Field
        label={t("auth.password")}
        type="password"
        bind:value={password}
        autocomplete="new-password"
        hint={t("auth.password_hint")}
        required
      />
      <Field label={t("auth.password_repeat")} type="password" bind:value={password2} autocomplete="new-password" required />
      <h3>{t("setup.admin.totp_title")}</h3>
      <p>{t("setup.admin.totp_help")}</p>
      {#if totpQR}<img class="qr" src={totpQR} alt={t("setup.admin.qr_alt")} width="220" height="220" />{/if}
      <p class="muted">{t("setup.admin.secret")} <code>{totpSecret}</code></p>
      <div class="row"><Button variant="ghost" onclick={newTotp}>{t("setup.admin.new_secret")}</Button></div>
      <Field label={t("auth.totp_code")} bind:value={totpCode} inputmode="numeric" autocomplete="one-time-code" required />
    {:else if step === "database"}
      <h2 tabindex="-1">{t("setup.db.title")}</h2>
      <fieldset class="stack">
        <legend class="sr-only">{t("setup.db.title")}</legend>
        <label class="choice"
          ><input type="radio" bind:group={dialect} value="sqlite" /> <span><strong>SQLite</strong> — {t("setup.db.sqlite")}</span></label
        >
        <label class="choice"
          ><input type="radio" bind:group={dialect} value="postgres" />
          <span><strong>PostgreSQL</strong> — {t("setup.db.postgres")}</span></label
        >
      </fieldset>
      {#if dialect === "postgres"}
        <Field
          label={t("setup.db.dsn")}
          bind:value={dsn}
          placeholder="postgres://simpleapb:…@postgres:5432/simpleapb?sslmode=disable"
          required
        />
      {/if}
      <div class="row"><Button variant="secondary" {busy} onclick={testDb}>{t("setup.test")}</Button></div>
    {:else if step === "library"}
      <h2 tabindex="-1">{t("setup.library.title")}</h2>
      <Field label={t("setup.library.path")} bind:value={libraryPath} hint={t("setup.library.hint")} required />
      <div class="row"><Button variant="secondary" {busy} onclick={testLibrary}>{t("setup.test")}</Button></div>
    {:else if step === "url"}
      <h2 tabindex="-1">{t("setup.url.title")}</h2>
      <Field label={t("setup.url.label")} type="url" bind:value={baseUrl} hint={t("setup.url.hint")} required />
    {:else if step === "smtp"}
      <h2 tabindex="-1">{t("setup.smtp.title")}</h2>
      <p>{t("setup.smtp.help")}</p>
      <label class="choice"><input type="checkbox" bind:checked={smtpOn} /> <span>{t("setup.smtp.enable")}</span></label>
      {#if smtpOn}
        <Field label={t("setup.smtp.host")} bind:value={smtp.host} required />
        <Field label={t("setup.smtp.port")} bind:value={smtp.port} inputmode="numeric" required />
        <label class="select"
          >{t("setup.smtp.tls")}
          <select bind:value={smtp.tls}
            ><option value="starttls">STARTTLS</option><option value="tls">TLS</option><option value="none">{t("setup.smtp.none")}</option
            ></select
          >
        </label>
        <Field label={t("setup.smtp.user")} bind:value={smtp.username} autocomplete="off" />
        <Field label={t("setup.smtp.password")} type="password" bind:value={smtp.password} autocomplete="off" />
        <Field label={t("setup.smtp.from")} type="email" bind:value={smtp.from} required />
        <Field label={t("setup.smtp.test_to")} type="email" bind:value={smtpTestTo} />
        <div class="row"><Button variant="secondary" {busy} disabled={!smtpTestTo} onclick={testSmtp}>{t("setup.smtp.test")}</Button></div>
      {/if}
    {:else if step === "ntfy"}
      <h2 tabindex="-1">{t("setup.ntfy.title")}</h2>
      <p>{t("setup.ntfy.help")}</p>
      <Field label={t("setup.ntfy.internal")} bind:value={ntfyInternal} hint={t("setup.ntfy.internal_hint")} />
      <Field label={t("setup.ntfy.public")} type="url" bind:value={ntfyPublic} hint={t("setup.ntfy.public_hint")} />
      <Field label={t("setup.ntfy.token")} bind:value={ntfyToken} hint={t("setup.ntfy.token_hint")} autocomplete="off" />
    {:else if step === "instance"}
      <h2 tabindex="-1">{t("setup.instance.title")}</h2>
      <Field label={t("setup.instance.name")} bind:value={instanceName} required />
      <label class="choice"><input type="checkbox" bind:checked={signupOpen} /> <span>{t("setup.instance.signup_open")}</span></label>
      <Alert kind="info">{t("setup.instance.legal_warning")}</Alert>
      <Field label={t("setup.instance.privacy")} bind:value={privacyText} multiline rows={10} />
      <Field label={t("setup.instance.terms")} bind:value={termsText} multiline rows={6} />
    {:else if step === "bandwidth"}
      <h2 tabindex="-1">{t("setup.bandwidth.title")}</h2>
      <Field label={t("setup.bandwidth.egress")} bind:value={egress} inputmode="numeric" hint={t("setup.bandwidth.hint")} />
    {:else if step === "review"}
      <h2 tabindex="-1">{t("setup.review.title")}</h2>
      <dl class="review">
        <dt>{t("auth.username")}</dt>
        <dd>{username}</dd>
        <dt>{t("setup.db.title")}</dt>
        <dd>{dialect === "sqlite" ? "SQLite" : "PostgreSQL"}</dd>
        <dt>{t("setup.library.path")}</dt>
        <dd><code>{libraryPath}</code></dd>
        <dt>{t("setup.url.label")}</dt>
        <dd>{baseUrl}</dd>
        <dt>{t("setup.smtp.title")}</dt>
        <dd>{smtpOn ? smtp.host : t("setup.review.off")}</dd>
        <dt>{t("setup.instance.name")}</dt>
        <dd>{instanceName}</dd>
        <dt>{t("setup.bandwidth.egress")}</dt>
        <dd>{egress} Mbps</dd>
      </dl>
    {/if}

    {#if error}<Alert kind="error">{error}</Alert>{/if}
    {#if notice}<Alert kind="ok">{notice}</Alert>{/if}

    {#if step !== "token"}
      <div class="row nav">
        <Button variant="ghost" onclick={back}>{t("common.back")}</Button>
        {#if step === "review"}
          <Button {busy} onclick={complete}>{t("setup.review.complete")}</Button>
        {:else if step === "admin"}
          <Button onclick={checkAdmin}>{t("common.continue")}</Button>
        {:else if step === "library"}
          <Button onclick={async () => (await testLibrary()) && next()}>{t("common.continue")}</Button>
        {:else}
          <Button onclick={next}>{t("common.continue")}</Button>
        {/if}
      </div>
    {/if}
  {/if}
</AuthShell>

<style>
  .qr {
    background: #fff;
    padding: 8px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
  }
  .choice {
    display: flex;
    gap: 0.75rem;
    align-items: flex-start;
    min-height: 48px;
    cursor: pointer;
  }
  .choice input {
    width: 1.25rem;
    height: 1.25rem;
    margin-top: 0.2rem;
  }
  fieldset {
    border: 0;
    padding: 0;
    margin: 0;
  }
  .select {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    font-weight: 600;
  }
  select {
    min-height: 48px;
    background: var(--surface-2);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 0 0.6rem;
    font: inherit;
  }
  .nav {
    justify-content: space-between;
  }
  .codes {
    columns: 2;
    font-size: 1.1rem;
    background: var(--surface-2);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 1rem 1rem 1rem 2.5rem;
  }
  .review {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.4rem 1rem;
    margin: 0;
  }
  dt {
    font-weight: 600;
  }
  dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  h2:focus {
    outline: none;
  }
</style>
