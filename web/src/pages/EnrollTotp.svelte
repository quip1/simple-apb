<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Shown when an account must use two-factor login but hasn't set it up yet. -->
<script lang="ts">
  import AuthShell from "../components/AuthShell.svelte";
  import TotpSetup from "../components/TotpSetup.svelte";
  import { t } from "../lib/i18n";

  let { required = false, ondone }: { required?: boolean; ondone?: () => void } = $props();
</script>

<AuthShell title={t("totp.title")}>
  <TotpSetup {required} {ondone} />
</AuthShell>
