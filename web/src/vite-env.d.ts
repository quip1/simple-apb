/// <reference types="svelte" />
/// <reference types="vite/client" />

declare const __APP_VERSION__: string;
/** True in the Windows app's build (desktop/). */
declare const __DESKTOP__: boolean;

// hls.js ships types only for its full build; the light build has the same API.
declare module "hls.js/light" {
  export { default } from "hls.js";
}
