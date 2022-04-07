const path = require("path");
const remarkCodeImport = require("remark-code-import");

/** @type {import('@docusaurus/types').DocusaurusConfig} */
module.exports = {
  title: "Dagger",
  tagline: "Dagger is a programmable deployment system",
  url: "https://docs.dagger.io",
  baseUrl: "/",
  onBrokenLinks: "warn",
  onBrokenMarkdownLinks: "warn",
  favicon: "img/favicon.png",
  organizationName: "Dagger",
  projectName: "Dagger",
  stylesheets: [
    "https://fonts.googleapis.com/css2?family=Karla&family=Montserrat:wght@700&display=swap",
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
          type: "docsVersionDropdown",
          position: "left",
          dropdownActiveClassDisabled: true,
        },
        {
          type: "search",
          position: "right",
        },
        {
          position: "right",
          label: "Discord",
          href: "https://discord.gg/ufnyBtc8uY",
          className: "header-discord-link",
          "aria-label": "Discord community",
        },
        {
          position: "right",
          label: "Github",
          href: "https://github.com/dagger/dagger",
          className: "header-github-link hide-target-icon",
          "aria-label": "GitHub repository",
        },
      ],
      hideOnScroll: true,
    },
    algolia: {
      apiKey: "559dcddb4378b889baa48352394616ec",
      indexName: "Dagger_docs",
      appId: "XSSC1LRN4S",
    },
    hotjar: {
      siteId: "2541514",
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
          lastVersion: "current",
          versions: {
            current: {
              label: "0.2",
            },
            0.1: {
              label: "0.1",
            },
          },
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
      },
    ],
  ],
  plugins: [
    "docusaurus-plugin-sass",
    [
      "docusaurus2-dotenv",
      {
        systemvars: true,
        expand: true,
      },
    ],
    path.resolve(__dirname, "plugins/docusaurus-plugin-hotjar"),
    [
      "@docusaurus/plugin-client-redirects",
      {
        redirects: [
          // /docs/oldDoc -> /docs/newDoc
          {
            to: "/0.1/1001/install/",
            from: "/1001/install/",
          },
          {
            to: "/0.1/1002/vs/",
            from: "/1002/vs/",
          },
          {
            to: "/0.1/1003/get-started/",
            from: "/1003/get-started/",
          },
          {
            to: "/0.1/1004/dev-first-env/",
            from: "/1004/dev-first-env/",
          },
          {
            to: "/0.1/1005/what-is-cue/",
            from: "/1005/what-is-cue/",
          },
          {
            to: "/0.1/1006/google-cloud-run/",
            from: "/1006/google-cloud-run/",
          },
          {
            to: "/0.1/1007/kubernetes/",
            from: "/1007/kubernetes/",
          },
          {
            to: "/0.1/1008/aws-cloudformation/",
            from: "/1008/aws-cloudformation/",
          },
          {
            to: "/0.1/1009/github-actions/",
            from: "/1009/github-actions/",
          },
          {
            to: "/0.1/1010/dev-cue-package/",
            from: "/1010/dev-cue-package/",
          },
          {
            to: "/0.1/1011/package-manager/",
            from: "/1011/package-manager/",
          },
          {
            to: "/0.1/1012/ci",
            from: "/1012/ci",
          },
          {
            to: "/0.1/1013/operator-manual/",
            from: "/1013/operator-manual/",
          },
        ],
      },
    ],
  ],
};
