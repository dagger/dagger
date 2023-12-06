module.exports = {
  extends: [
    require.resolve("../../sdk/nodejs/.eslintrc.cjs"),
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
