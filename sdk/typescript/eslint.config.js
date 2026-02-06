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
    files: ["src/**/*.ts"],
    languageOptions: {
      parser: tseslint.parser,
      parserOptions: {
        // Resolve project paths from this file
        tsconfigRootDir: import.meta.dirname,
      },
    },
  },
  {
    ignores: [
      "dist/",
      "**/testdata/**",
      "dev/**",
      "runtime/template/src/**",
      "*.cjs",
      ".changie.yaml",
      "**/*.md",
      "telemetry/",
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
