// @vitest-environment happy-dom
// Unit tests for providerFieldValue, the settings-dialog value resolver for
// provider setting fields. The rule it encodes is server parity: absent means
// "use the schema default" (provider.NormalizeSettings does the same before a
// factory runs), present means "this is the user's value" — including a bare
// `key:` in YAML, which arrives as null and which the server also counts as
// set. A regression here is silent: the dialog renders the wrong state and the
// next save writes it back as a deliberate choice.
import { describe, it, expect } from "vitest";
import { providerFieldValue } from "./config-providers.js";
import type { SchemaField } from "./api-types.js";

const boolDefaultTrue: SchemaField = {
  key: "use_hash",
  label: "Use Hash",
  type: "bool",
  default: "true",
};

const boolDefaultFalse: SchemaField = {
  key: "include_machine_translated",
  label: "Include Machine Translated",
  type: "bool",
  default: "false",
};

const secretNoDefault: SchemaField = {
  key: "api_key",
  label: "API Key",
  type: "secret",
  secret: true,
};

describe("providerFieldValue", () => {
  const cases: {
    name: string;
    field: SchemaField;
    settings: Record<string, unknown> | undefined;
    want: string;
  }[] = [
    {
      name: "absent key falls back to a true default",
      field: boolDefaultTrue,
      settings: {},
      want: "true",
    },
    {
      name: "missing settings block falls back to the default",
      field: boolDefaultTrue,
      settings: undefined,
      want: "true",
    },
    {
      name: "absent key falls back to a false default",
      field: boolDefaultFalse,
      settings: {},
      want: "false",
    },
    {
      name: "absent key with no default resolves empty",
      field: secretNoDefault,
      settings: {},
      want: "",
    },
    {
      name: "explicit false beats a true default",
      field: boolDefaultTrue,
      settings: { use_hash: false },
      want: "false",
    },
    {
      name: "explicit true is kept",
      field: boolDefaultFalse,
      settings: { include_machine_translated: true },
      want: "true",
    },
    {
      name: "null counts as set, matching the server",
      field: boolDefaultTrue,
      settings: { use_hash: null },
      want: "",
    },
    {
      name: "empty secret stays empty, never the default",
      field: secretNoDefault,
      settings: { api_key: "" },
      want: "",
    },
    {
      name: "string value passes through",
      field: secretNoDefault,
      settings: { api_key: "abc123" },
      want: "abc123",
    },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      expect(providerFieldValue(tc.field, tc.settings)).toBe(tc.want);
    });
  }
});
