import dts from "rollup-plugin-dts"

export default {
  input: "./dist/src/index.d.ts", // or wherever your entrypoint is
  output: {
    file: "dist/core.d.ts",
    format: "es",
  },
  plugins: [
    dts({
      respectExternal: false,
      // Help the resolver find the workspace package's declarations
      compilerOptions: {
        baseUrl: ".",
        paths: {
          "@dagger.io/telemetry": ["./telemetry/dist/index.d.ts"],
        },
      },
    }),
  ],
}
