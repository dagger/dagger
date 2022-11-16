module.exports = {
  extends: ["eslint:recommended", "plugin:@typescript-eslint/recommended"],
  ignorePatterns: [
    "dist/",

    // FIXME: generated files should be linted
    "api/client.gen.ts",
    "api/types.ts",
  ],
  parser: "@typescript-eslint/parser",
  plugins: ["@typescript-eslint"],
  root: true,
};
