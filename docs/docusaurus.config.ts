import type { Config } from "@docusaurus/types";
import type * as Preset from "@docusaurus/preset-classic";
import path from "path";
import { themes as prismThemes } from "prism-react-renderer";
import remarkCodeImport from "remark-code-import";

const config: Config = {
  title: "Dagger",
  favicon: "img/favicon.png",

  // Set the production url of your site here
  url: "https://docs.dagger.io",
  // Set the /<baseUrl>/ pathname under which your site is served
  // For GitHub pages deployment, it is often '/<projectName>/'
  baseUrl: "/",

  // GitHub pages deployment config.
  // If you aren't using GitHub pages, you don't need these.
  //organizationName: 'facebook', // Usually your GitHub org/user name.
  //projectName: 'docusaurus', // Usually your repo name.

  onBrokenLinks: "throw",
  onBrokenMarkdownLinks: "throw",

  // Even if you don't use internationalization, you can use this field to set
  // useful metadata like html lang. For example, if your site is Chinese, you
  // may want to replace "en" with "zh-Hans".
  i18n: {
    defaultLocale: "en",
    locales: ["en"],
  },
  markdown: {
    mermaid: true,
  },
  presets: [
    [
      "classic",
      {
        docs: {
          breadcrumbs: false,
          path: "./current_docs",
          routeBasePath: "/",
          sidebarPath: "./sidebars.ts",
          editUrl: "https://github.com/dagger/dagger/edit/main/docs",
          remarkPlugins: [
            [remarkCodeImport, { allowImportingFromOutside: true }],
          ],
        },
        blog: false,
        theme: {
          customCss: require.resolve("./src/css/custom.scss"),
        },
      } satisfies Preset.Options,
    ],
  ],
  plugins: [
    "docusaurus-plugin-sass",
    "docusaurus-plugin-image-zoom",
    [
      path.resolve(__dirname, "plugins/docusaurus-plugin-guides/index.ts"),
      {
        versions: [
          {
            guidesPath: "./current_docs/guides",
            versionName: "current",
          },
        ],
      },
    ],
    [
      "posthog-docusaurus",
      {
        appUrl: "https://dagger.io/analytics",
        apiKey: "phc_rykA1oJnBnxTwavpgJKr4RAVXEgCkpyPVi21vQ7906d",
      },
    ],
    [
      "docusaurus-plugin-typedoc",
      {
        id: "current-generation",
        entryPoints: [
          "../sdk/typescript/connect.ts",
          "../sdk/typescript/api/client.gen.ts",
          "../sdk/typescript/common/errors/index.ts",
        ],
        tsconfig: "../sdk/typescript/tsconfig.json",
        out: "../current_docs/reference/typescript/",
        excludeProtected: true,
        exclude: "../sdk/typescript/node_modules/**",
        skipErrorChecking: true,
        disableSources: true,
        sidebar: {
          categoryLabel: "TypeScript SDK Reference",
        },
        frontmatter: {
          displayed_sidebar: "current",
          sidebar_label: "TypeScript SDK Reference",
          title: "TypeScript SDK Reference",
        },
        hideMembersSymbol: true,
        requiredToBeDocumented: ["Class"],
      },
    ],
  ],
  themes: ["@docusaurus/theme-mermaid"],
  themeConfig: {
    sidebarCollapsed: false,
    metadata: [{ name: "og:image", content: "/img/favicon.png" }],
    prism: {
      additionalLanguages: ["php", "rust", "elixir", "bash", "toml"],
      theme: prismThemes.dracula,
    },
    announcementBar: {
      id: "changed_docs",
      content:
        'We\'ve recently updated our documentation. For the previous documentation, visit <a target="_blank" rel="noopener noreferrer" href="https://archive.docs.dagger.io/0.9/">archive.docs.dagger.io/0.9/</a>.',
      backgroundColor: "#3d66ff",
      textColor: "#ffffff",
      isCloseable: false,
    },
    navbar: {
      logo: {
        alt: "Dagger Logo",
        src: "img/dagger-logo-white.svg",
        height: "50px",
        href: "https://docs.dagger.io/",
      },
      items: [
        {
          position: "right",
          to: "https://dagger.io/blog",
          label: "Blog",
          className: "navbar-blog-link",
        },
        {
          position: "right",
          href: "https://github.com/dagger/dagger",
          className: "header-github-link hide-target-icon",
          "aria-label": "GitHub repository",
        },
        {
          position: "right",
          href: "https://discord.gg/ufnyBtc8uY",
          className: "header-discord-link",
        },
        {
          type: "search",
          position: "right",
          className: "header-searchbar",
        },
      ],
    },
    algolia: {
      apiKey: "bffda1490c07dcce81a26a144115cc02",
      indexName: "dagger",
      appId: "XEIYPBWGOI",
    },
    colorMode: {
      defaultMode: "light",
    },
    zoom: {
      selector: ".markdown img:not(.not-zoom)",
      background: {
        light: "rgb(255, 255, 255)",
        dark: "rgb(50, 50, 50)",
      },
      // medium-zoom configuration options
      // Refer to https://github.com/francoischalifour/medium-zoom#options
      config: {},
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
