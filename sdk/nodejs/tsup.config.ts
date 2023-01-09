import { defineConfig } from "tsup"

export default defineConfig({
  entry: ["src/index.ts"],
  splitting: false,
  sourcemap: true,
  clean: true,
  format: ["cjs", "esm"],
  dts: true,
  treeshake: true,
  outExtension({ format }) {
    return {
      js: `.${FORMAT_EXTENSION[format]}`,
    }
  },
})

const FORMAT_EXTENSION = {
  esm: "mjs",
  cjs: "cjs",
  iife: "global.js",
} as const
