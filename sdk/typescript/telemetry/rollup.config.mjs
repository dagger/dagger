import commonjs from "@rollup/plugin-commonjs"
import json from "@rollup/plugin-json"
import resolve from "@rollup/plugin-node-resolve"
import typescript from "@rollup/plugin-typescript"
import dts from "rollup-plugin-dts"

const external = (id) =>
  id.startsWith("@opentelemetry/") ||
  // also keep Node builtins external for a library
  id === "node:fs" ||
  id === "node:path" ||
  id === "node:crypto" ||
  id === "fs" ||
  id === "path" ||
  id === "crypto"

const pluginsBase = [
  resolve({ extensions: [".mjs", ".js", ".json", ".ts"] }),
  commonjs(),
  json(),
]

// 1) ESM build (Node-friendly baseline)
const esm = {
  input: "src/index.ts",
  external,
  output: {
    dir: "dist/esm",
    format: "esm",
    sourcemap: true,
    entryFileNames: "[name].js",
  },
  plugins: [
    ...pluginsBase,
    typescript({
      outDir: "./dist/esm",
      tsconfig: "./tsconfig.json",
      target: "ES2020",
    }),
  ],
}

// 2) CJS build (use .cjs for safety with "type": "module")
const cjs = {
  input: "src/index.ts",
  external,
  output: {
    dir: "dist/cjs",
    format: "cjs",
    sourcemap: true,
    exports: "named",
    entryFileNames: "[name].cjs",
  },
  plugins: [
    ...pluginsBase,
    typescript({
      outDir: "./dist/cjs",
      tsconfig: "./tsconfig.json",
      target: "ES2020",
    }),
  ],
}

// 3) ESNEXT build (modern ESM for bundlers / evergreen)
const esnext = {
  input: "src/index.ts",
  external,
  output: {
    dir: "dist/esnext",
    format: "esm",
    sourcemap: true,
    entryFileNames: "[name].js",
  },
  plugins: [
    ...pluginsBase,
    typescript({
      outDir: "./dist/esnext",
      tsconfig: "./tsconfig.json",
      target: "ESNext",
    }),
  ],
}

// 4) Types bundle (single d.ts entry)
const types = {
  input: "dist/types/index.d.ts",
  output: {
    file: "dist/index.d.ts",
    format: "esm",
  },
  plugins: [dts()],
  external,
}

export default [esm, cjs, esnext, types]
