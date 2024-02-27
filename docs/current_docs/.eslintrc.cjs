module.exports = {
  extends: [
    require.resolve("../../sdk/typescript/.eslintrc.cjs"),
  ],
  overrides: [
    {
      files: ["lambda.js"],
      rules: {
        "@typescript-eslint/no-unused-vars": "off",
      },
    }
  ],
}
