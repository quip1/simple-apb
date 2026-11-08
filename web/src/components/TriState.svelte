<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // Inherit / Allow / Deny for one permission override.
  import { t } from "../lib/i18n";

  let { label, value = $bindable(), effective }: { label: string; value: boolean | undefined; effective?: boolean } = $props();

  const str = $derived(value === undefined ? "inherit" : value ? "allow" : "deny");
</script>

<label class="tri">
  <span>{label}</span>
  <select
    value={str}
    onchange={(e) => {
      const v = e.currentTarget.value;
      value = v === "inherit" ? undefined : v === "allow";
    }}
  >
    <option value="inherit">{t("admin.perm.inherit")}</option>
    <option value="allow">{t("admin.perm.allow")}</option>
    <option value="deny">{t("admin.perm.deny")}</option>
  </select>
  {#if effective !== undefined}
    <span class="muted eff">{effective ? t("admin.perm.now_allowed") : t("admin.perm.now_denied")}</span>
  {/if}
</label>

<style>
  .tri {
    display: grid;
    grid-template-columns: minmax(10rem, 1fr) 10rem minmax(7rem, auto);
    gap: 0.75rem;
    align-items: center;
  }
  .eff {
    font-size: 0.85rem;
  }
  @media (max-width: 36rem) {
    .tri {
      grid-template-columns: 1fr;
      gap: 0.25rem;
    }
  }
</style>
