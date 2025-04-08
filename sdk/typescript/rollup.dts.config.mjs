import dts from "rollup-plugin-dts"

export default {
  input: "./dist/src/index.d.ts", // or wherever your entrypoint is
  output: {
    file: "dist/core.d.ts",
    format: "es",
  },
  plugins: [dts()],
}
