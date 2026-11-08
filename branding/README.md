<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
# Branding

Chosen in decision #9 (ADR-0038): the **Fern** accent (`#94d68f`, label text `#0b230c`) and the
**Open book** icon (an open book with sound rising from it).

| File | Use |
|---|---|
| `icon.svg` | Colour icon (Fern on `#0f1115`). The web favicon is a copy in `web/public/favicon.svg`. |
| `icon-eink.svg` | Monochrome e-ink version, black on white. |
| `android/ic_launcher*.xml` | Android adaptive icon: background, foreground and themed (monochrome) layers, plus `mipmap-anydpi-v26/ic_launcher.xml`. Copied into the app module in Phase 3. |

The artwork sits in a 108-unit square; the Android foreground is scaled to 80 % so it stays inside
the 66 dp safe zone.
