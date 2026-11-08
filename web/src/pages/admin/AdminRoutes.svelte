<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  // The admin panel's routes. Loaded on demand, so listeners never download it.
  import { t } from "../../lib/i18n";
  import { match, route } from "../../lib/router.svelte";
  import Audit from "./Audit.svelte";
  import Backups from "./Backups.svelte";
  import BookAdmin from "./BookAdmin.svelte";
  import Dashboard from "./Dashboard.svelte";
  import Groups from "./Groups.svelte";
  import Inbox from "./Inbox.svelte";
  import InstanceSettings from "./InstanceSettings.svelte";
  import MasterLibrary from "./MasterLibrary.svelte";
  import Tags from "./Tags.svelte";
  import Trash from "./Trash.svelte";
  import UserDetail from "./UserDetail.svelte";
  import Users from "./Users.svelte";

  const user = $derived(match("/admin/users/:id", route.path));
  const book = $derived(match("/admin/books/:id", route.path));
</script>

{#if match("/admin", route.path)}
  <Dashboard />
{:else if match("/admin/inbox", route.path)}
  <Inbox />
{:else if match("/admin/users", route.path)}
  <Users />
{:else if user}
  {#key user.id}<UserDetail id={user.id!} />{/key}
{:else if match("/admin/groups", route.path)}
  <Groups />
{:else if match("/admin/tags", route.path)}
  <Tags />
{:else if match("/admin/library", route.path)}
  <MasterLibrary />
{:else if book}
  {#key book.id}<BookAdmin id={book.id!} />{/key}
{:else if match("/admin/trash", route.path)}
  <Trash />
{:else if match("/admin/audit", route.path)}
  <Audit />
{:else if match("/admin/backups", route.path)}
  <Backups />
{:else if match("/admin/settings", route.path)}
  <InstanceSettings />
{:else}
  <h1>{t("error.not_found_title")}</h1>
{/if}
