import js from "@eslint/js"
import eslintConfigPrettier from "eslint-config-prettier"
import eslintPluginPrettierRecommended from "eslint-plugin-prettier/recommended"
import tseslint from "typescript-eslint"

export default [
  js.configs.recommended,
  eslintConfigPrettier,
  eslintPluginPrettierRecommended,
  ...tseslint.configs.recommended,
  {
    ignores: [
      "dist/",
      // packages/core is copied verbatim from dagger/typescript-sdk and
      // regenerated there against a running engine — client.gen.ts alone is 23k
      // lines. Linting or reformatting it here would be a local edit that
      // vanishes on the next copy, which is what its README forbids.
      "packages/core/**",
      "**/testdata/**",
      "dev/**",
      "runtime/template/src/**",
      "*.cjs",
      ".changie.yaml",
      "**/*.md",
    ],
  },
  {
    files: ["sdk/typescript/src/api/client.gen.ts", "src/api/client.gen.ts"],
    rules: {
      "@typescript-eslint/no-unused-vars": "off",
      "@typescript-eslint/no-duplicate-enum-values": "off",
    },
  },
]
