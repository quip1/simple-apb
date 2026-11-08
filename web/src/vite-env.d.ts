/// <reference types="svelte" />
/// <reference types="vite/client" />

declare const __APP_VERSION__: string;

// hls.js ships types only for its full build; the light build has the same API.
declare module "hls.js/light" {
  export { default } from "hls.js";
}
