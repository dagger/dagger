const fs = require("node:fs");
const path = require("node:path");

const localPlugin = require("../shared/local-plugin.js");
const externalAssetsDir = path.join(__dirname, "../shared-assets");
const externalAssetEntries = fs.readdirSync(externalAssetsDir);

/** @type {import('@docusaurus/types').Config} */
module.exports = {
  title: "Runtime Format Fixture",
  url: "https://example.com",
  baseUrl: "/",
  onBrokenLinks: "ignore",
  onBrokenMarkdownLinks: "ignore",
  presets: [
    [
      "classic",
      {
        docs: {
          routeBasePath: "/",
          sidebarPath: false,
        },
        blog: false,
        pages: false,
      },
    ],
  ],
  plugins: [localPlugin],
  customFields: {
    externalAssetEntries,
  },
};
