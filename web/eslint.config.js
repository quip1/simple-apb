// SPDX-License-Identifier: AGPL-3.0-or-later
import js from "@eslint/js";
import prettier from "eslint-config-prettier";
import svelte from "eslint-plugin-svelte";
import globals from "globals";
import ts from "typescript-eslint";
import svelteConfig from "./svelte.config.js";

export default ts.config(
  { ignores: ["dist/", "dist-desktop/", "playwright-report/", "test-results/", "node_modules/", "src/api/schema.gen.ts"] },
  js.configs.recommended,
  ...ts.configs.recommended,
  ...svelte.configs.recommended,
  prettier,
  ...svelte.configs.prettier,
  {
    languageOptions: { globals: { ...globals.browser, __APP_VERSION__: "readonly" } },
  },
  {
    files: ["**/*.svelte", "**/*.svelte.ts"],
    languageOptions: {
      parserOptions: { projectService: true, extraFileExtensions: [".svelte"], parser: ts.parser, svelteConfig },
    },
  },
  {
    files: ["scripts/**", "*.config.*"],
    languageOptions: { globals: globals.node },
  },
  {
    rules: {
      "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_", varsIgnorePattern: "^_", caughtErrorsIgnorePattern: "^_" }],
      // Navigation goes through our own router (`link` action / navigate()).
      "svelte/no-navigation-without-resolve": "off",
      // Flags short-lived temporaries (a Set inside a function); reactive state uses $state.
      "svelte/prefer-svelte-reactivity": "off",
    },
  },
);
