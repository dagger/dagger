const path = require("path");
const remarkCodeImport = require("remark-code-import");
const mdxMermaid = require("mdx-mermaid");
/** @type {import('@docusaurus/types').DocusaurusConfig} */
module.exports = {
  title: "Dagger",
  tagline: "",
  url: "https://docs.dagger.io",
  baseUrl: "/",
  onBrokenMarkdownLinks: "warn",
  favicon: "img/favicon.png",
  organizationName: "Dagger",
  projectName: "Dagger",
  stylesheets: [
    "https://fonts.googleapis.com/css2?family=Source+Code+Pro:wght@400&display=swap",
  ],
  customFields: {},
  themeConfig: {
    sidebarCollapsed: false,
    prism: {
      theme: require("prism-react-renderer/themes/okaidia"),
    },
    navbar: {
      logo: {
        alt: "Dagger Logo",
        src: "img/dagger-logo.png",
      },
      items: [
        {
          type: "search",
          position: "right",
          className: "header-searchbar",
        },
        {
          position: "right",
          to: "https://dagger.io/blog",
          label: "Blog",
          className: "navbar-blog-link",
        },
        {
          position: "right",
          type: "html",
          value: "<span></span>",
          className: "navbar-items-separator",
        },
        {
          position: "right",
          href: "https://github.com/dagger/dagger",
          className: "header-github-link hide-target-icon",
          "aria-label": "GitHub repository",
        },
        {
          position: "right",
          type: "html",
          value:
            "<a href='https://discord.com/channels/707636530424053791/1003718839739105300'><div></div><span>Dagger Discord</span></a>",
          className: "header-discord-link",
        },
        {
          position: "right",
          type: "html",
          value: "<span></span>",
          className: "navbar-items-separator",
        },
      ],
      hideOnScroll: true,
    },
    colorMode: {
      defaultMode: "light",
    },
  },
  presets: [
    [
      "@docusaurus/preset-classic",
      {
        docs: {
          breadcrumbs: false,
          path: "../docs-0.3",
          sidebarPath: require.resolve("./sidebars.js"),
          editUrl: "https://github.com/dagger/dagger/edit/main/website",
          routeBasePath: "/",
          remarkPlugins: [remarkCodeImport, mdxMermaid],
        },
        theme: {
          customCss: require.resolve("./src/css/custom.scss"),
        },
        blog: false,
      },
    ],
  ],
  plugins: [
    "docusaurus-plugin-sass",
    "docusaurus2-dotenv", 
    [ 
      "posthog-docusaurus",
      {
        apiKey: "phc_8Onnz5zyGA8mMEia4ALiaAetunwfeoHiekU0l5ND6tg"
      }
    ]
  ]
};
