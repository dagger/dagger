const path = require("path");

async function createConfig() {
  const remarkCodeImport = (await import('remark-code-import')).codeImport;
  return {
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
      "https://fonts.googleapis.com/css2?family=Montserrat:wght@500;700&family=Source+Code+Pro:wght@400&display=swap",
    ],
    customFields: {
      AMPLITUDE_ID: process.env.REACT_APP_AMPLITUDE_ID,
    },
    themeConfig: {
      sidebarCollapsed: false,
      metadata: [{name: 'og:image', content: '/img/favicon.png'}],
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
    markdown: {
      mermaid: true,
    },
    themes: ['@docusaurus/theme-mermaid'],
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
            remarkPlugins: [
              [remarkCodeImport, {allowImportingFromOutside: true}],
            ]
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
      [
        "posthog-docusaurus",
        {
          apiKey: "phc_rykA1oJnBnxTwavpgJKr4RAVXEgCkpyPVi21vQ7906d"
        }
      ],
      "docusaurus-plugin-image-zoom",
      path.resolve(__dirname, "plugins/docusaurus-plugin-hotjar"),
      path.resolve(__dirname, "plugins/docusaurus-plugin-dagger-version"),
      "docusaurus-plugin-includes",
      [
        "docusaurus-plugin-typedoc",
        {
          entryPoints: ['../sdk/nodejs/connect.ts', '../sdk/nodejs/api/client.gen.ts', '../sdk/nodejs/common/errors/index.ts'],
          tsconfig: '../sdk/nodejs/tsconfig.json',
          out: '../../docs/current/sdk/nodejs/reference/',
          excludeProtected: true,
          skipErrorChecking: true,
          disableSources: true,
          sidebar: {
            categoryLabel: 'Reference',
          },
          frontmatter: {
            displayed_sidebar: 'current',
            sidebar_label: 'Reference',
            title: "Dagger NodeJS SDK"
          },
          hideMembersSymbol: true,
          requiredToBeDocumented: ["Class"]
        },
      ]
    ],
  }
}

/** @type {import('@docusaurus/types').DocusaurusConfig} */
module.exports = createConfig;
