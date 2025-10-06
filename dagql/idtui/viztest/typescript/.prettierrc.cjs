module.exports = {
  "semi": false,
  "printWidth": 80,
  "useTabs": false,
  "tabWidth": 2,
  "singleQuote": false,
  "bracketSpacing": true,
  "arrowParens": "always",
  "importOrder": ["^[./]"],
  "importOrderSeparation": true,
  "importOrderParserPlugins": ["typescript", "decorators-legacy"],
  "plugins": [require.resolve("@trivago/prettier-plugin-sort-imports")],
}
