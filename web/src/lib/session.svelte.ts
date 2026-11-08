// SPDX-License-Identifier: AGPL-3.0-or-later
//
// App-wide state: instance info and the signed-in user.

import { api, type Instance, type Me } from "../api/client";

export const session = $state<{
  loaded: boolean;
  instance: Instance | null;
  me: Me | null;
}>({ loaded: false, instance: null, me: null });

export async function loadSession(): Promise<void> {
  const inst = await api.GET("/instance");
  session.instance = inst.data ?? null;
  if (session.instance && !session.instance.setup_required) {
    const me = await api.GET("/me");
    session.me = me.data ?? null;
  }
  session.loaded = true;
}

export async function refreshMe(): Promise<void> {
  const me = await api.GET("/me");
  session.me = me.data ?? null;
}

export function signedOut(): void {
  session.me = null;
}

window.addEventListener("sapb:signed-out", signedOut);
