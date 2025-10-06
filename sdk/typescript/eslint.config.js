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
