# Additional permission under GNU AGPL version 3, section 7

Simple APB is licensed under the GNU Affero General Public License version 3
(see `LICENSE`). As the copyright holders, the Simple APB authors grant the
following additional permission under section 7 of that license.

## Google Play Services / Google Cast SDK linking exception

If you modify this Program, or any covered work, by linking or combining it
with the **Google Play Services client libraries** or the **Google Cast SDK**
(or modified versions of those libraries), containing parts covered by the
terms of their respective proprietary licenses, the licensors of this Program
grant you additional permission to convey the resulting work.

Corresponding Source for a non-source form of such a combination shall not
include the source code for the parts of those libraries used, provided that
the Corresponding Source of the rest of the work is still made available under
the AGPL-3.0.

## Scope

- This permission exists so the `play` and `github` Android build flavors can
  ship Chromecast support.
- The `fdroid` flavor must not include these libraries, and does not rely on
  this permission.
- All code that touches these libraries lives in the `android/cast/` module
  behind an interface, so the boundary stays auditable.

You may remove this additional permission from your own copy or modified
versions of the Program, in which case the Program is covered by the plain
AGPL-3.0 and the `play`/`github` flavors may no longer be conveyed with the
Cast SDK.
