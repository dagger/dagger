/** @type {import('@docusaurus/types').Config} */
module.exports = {
  title: "Runtime Preserve Fixture",
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
};
