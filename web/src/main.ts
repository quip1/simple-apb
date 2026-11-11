// SPDX-License-Identifier: AGPL-3.0-or-later
import { mount } from "svelte";
import App from "./App.svelte";
import { DESKTOP } from "./lib/desktop.svelte";
import "./styles/app.css";

const target = document.getElementById("app")!;
// The Windows app's mini-player window shows only the controls.
if (DESKTOP && location.pathname === "/mini") void import("./pages/desktop/MiniWindow.svelte").then((m) => mount(m.default, { target }));
else mount(App, { target });
