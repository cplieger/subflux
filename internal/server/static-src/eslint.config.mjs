// Strict typed-linting config.
// References:
//   https://typescript-eslint.io/users/configs/#strict-type-checked
//   https://typescript-eslint.io/users/configs/#stylistic-type-checked
//   https://typescript-eslint.io/getting-started/typed-linting

import js from "@eslint/js";
import tseslint from "typescript-eslint";

export default [
  // 1. Ignore generated/build outputs and configs that don't need linting.
  {
    ignores: [
      "node_modules/**",
      "dist/**",
      "build/**",
      "coverage/**",
      "**/*.min.js",
      "**/*.gen.ts",
      "wire/*.gen.ts",
      "test-stubs/**",
    ],
  },

  // 2. Strictest official preset combination (typed linting required).
  js.configs.recommended,
  ...tseslint.configs.strictTypeChecked,
  ...tseslint.configs.stylisticTypeChecked,

  // 3. Project setup — projectService auto-discovers tsconfig per file.
  {
    languageOptions: {
      ecmaVersion: 2024,
      sourceType: "module",
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
      globals: {
        // Browser
        window: "readonly",
        document: "readonly",
        navigator: "readonly",
        console: "readonly",
        fetch: "readonly",
        URL: "readonly",
        URLSearchParams: "readonly",
        setTimeout: "readonly",
        clearTimeout: "readonly",
        setInterval: "readonly",
        clearInterval: "readonly",
        requestAnimationFrame: "readonly",
        cancelAnimationFrame: "readonly",
        queueMicrotask: "readonly",
        WebSocket: "readonly",
        EventSource: "readonly",
        AbortController: "readonly",
        Headers: "readonly",
        Request: "readonly",
        Response: "readonly",
        FormData: "readonly",
        Blob: "readonly",
        File: "readonly",
        FileReader: "readonly",
        IntersectionObserver: "readonly",
        MutationObserver: "readonly",
        ResizeObserver: "readonly",
        CustomEvent: "readonly",
        Event: "readonly",
        EventTarget: "readonly",
        TextEncoder: "readonly",
        TextDecoder: "readonly",
        crypto: "readonly",
        btoa: "readonly",
        atob: "readonly",
        // Service Worker
        self: "readonly",
        ServiceWorkerGlobalScope: "readonly",
        clients: "readonly",
        // Test runner globals (vitest auto-injected)
        describe: "readonly",
        it: "readonly",
        test: "readonly",
        expect: "readonly",
        beforeAll: "readonly",
        beforeEach: "readonly",
        afterAll: "readonly",
        afterEach: "readonly",
        vi: "readonly",
      },
    },
    rules: {
      // Allow `_`-prefixed unused names.
      "@typescript-eslint/no-unused-vars": [
        "error",
        {
          argsIgnorePattern: "^_",
          varsIgnorePattern: "^_",
          caughtErrorsIgnorePattern: "^_",
          destructuredArrayIgnorePattern: "^_",
          ignoreRestSiblings: true,
        },
      ],
      // Enforce `import type {...}` for types.
      "@typescript-eslint/consistent-type-imports": [
        "error",
        { prefer: "type-imports", fixStyle: "inline-type-imports" },
      ],
      // Discourage `any`, prefer `unknown`.
      "@typescript-eslint/no-explicit-any": "error",
      // Avoid silent fall-through bugs in async event handlers.
      "@typescript-eslint/no-misused-promises": [
        "error",
        { checksVoidReturn: { attributes: false } },
      ],
      // Prefer literal numeric/string template parts (catches accidental coercion).
      "@typescript-eslint/restrict-template-expressions": [
        "error",
        { allowNumber: true, allowBoolean: true, allowNullish: false },
      ],
      // Console policy.
      "no-console": ["warn", { allow: ["warn", "error"] }],
      // Equality: enforce strict ===.
      eqeqeq: ["error", "always", { null: "ignore" }],
      curly: ["error", "all"],
      "no-var": "error",
      "prefer-const": "error",
      "no-throw-literal": "error",
    },
  },

  // 4. Tests: typed but with relaxed rules (tests deliberately break invariants).
  {
    files: [
      "**/*.test.ts",
      "**/*.fuzz.test.ts",
      "**/*.property.test.ts",
      "**/test-helpers/**",
      "**/__mocks__/**",
    ],
    rules: {
      "@typescript-eslint/no-non-null-assertion": "off",
      "@typescript-eslint/no-explicit-any": "off",
      "@typescript-eslint/no-unsafe-assignment": "off",
      "@typescript-eslint/no-unsafe-member-access": "off",
      "@typescript-eslint/no-unsafe-call": "off",
      "@typescript-eslint/no-unsafe-argument": "off",
      "@typescript-eslint/no-unsafe-return": "off",
      "@typescript-eslint/unbound-method": "off",
      "@typescript-eslint/require-await": "off",
      "no-console": "off",
    },
  },

  // 5. Generated and config files: drop type-checked rules.
  {
    files: [
      "**/*.gen.ts",
      "**/wire/*.ts",
      "vitest.config.ts",
      "*.config.ts",
      "*.config.mjs",
      "*.config.js",
    ],
    ...tseslint.configs.disableTypeChecked,
  },
];
