module.exports = {
  extends: [
    "eslint:recommended",
    "plugin:@typescript-eslint/recommended",
    "prettier",
    "plugin:prettier/recommended",
  ],
  ignorePatterns: ["dist/"],
  parser: "@typescript-eslint/parser",
  plugins: ["@typescript-eslint"],
  root: true,

  // Custom rules definitions
  rules: {
    // Override 'no unused var' rules to allow unused arguments if it's
    // named '_'.
    "no-unused-vars": "off",
    "@typescript-eslint/no-unused-vars": [
      "error",
      {
        "argsIgnorePattern": "_",
      },
    ]
  }

}
