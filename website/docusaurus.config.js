const path = require("path");
const remarkCodeImport = require("remark-code-import");

/** @type {import('@docusaurus/types').DocusaurusConfig} */
module.exports = {
  title: "Dagger",
  tagline: "Dagger is a programmable deployment system",
  url: "https://docs.dagger.io",
  baseUrl: "/",
  onBrokenMarkdownLinks: "throw",
  onBrokenLinks: "throw",
  favicon: "img/favicon.png",
  organizationName: "Dagger",
  projectName: "Dagger",
  stylesheets: [
    "https://fonts.googleapis.com/css2?family=Source+Code+Pro:wght@400&display=swap",
  ],
  customFields: {
    AMPLITUDE_ID: process.env.REACT_APP_AMPLITUDE_ID,
  },
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
            "<a href='https://discord.gg/ufnyBtc8uY'><div></div><span>Ask for help</span></a>",
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
    algolia: {
      apiKey: "bffda1490c07dcce81a26a144115cc02",
      indexName: "dagger",
      appId: "XEIYPBWGOI",
    },
    hotjar: {
      siteId: "2541514",
    },
    colorMode: {
      defaultMode: "light",
    },
    posthog: {
      apiKey: "phc_hqwS484sDJhTnrPCANTyWX48nKL3AEucgf6w0czQtQi",
    },
    zoom: {
      selector: '.markdown img:not(.not-zoom)',
      background: {
        light: 'rgb(255, 255, 255)',
        dark: 'rgb(50, 50, 50)'
      },
      // medium-zoom configuration options
      // Refer to https://github.com/francoischalifour/medium-zoom#options
      config: {}
    }
  },
  presets: [
    [
      "@docusaurus/preset-classic",
      {
        docs: {
          breadcrumbs: false,
          path: "../docs",
          sidebarPath: require.resolve("./sidebars.js"),
          editUrl: "https://github.com/dagger/dagger/edit/main/website",
          routeBasePath: "/",
          remarkPlugins: [remarkCodeImport],
        },
        gtag: {
          trackingID: "G-RDXG80F635",
          anonymizeIP: true,
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
    "posthog-docusaurus",
    "docusaurus-plugin-image-zoom",
    path.resolve(__dirname, "plugins/docusaurus-plugin-hotjar"),
    path.resolve(__dirname, "plugins/docusaurus-plugin-dagger-version"),
  ],
};
