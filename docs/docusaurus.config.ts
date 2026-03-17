import type { Config } from "@docusaurus/types";
import type * as Preset from "@docusaurus/preset-classic";
import { themes as prismThemes } from "prism-react-renderer";
import remarkCodeImport from "remark-code-import";
import remarkTemplate from "./plugins/remark-template";
import llmsTxtPlugin from "./plugins/llms-txt-plugin";
import path from "path";

import { daggerVersion } from "./current_docs/partials/version";

const url = "https://docs.dagger.io";

const config: Config = {
  title: "Dagger",
  tagline:
    "Open-source runtime for composable workflows, powering AI agents and CI/CD with modular, repeatable, and observable pipelines.",
  favicon: "img/favicon.svg",

  // Set the production url of your site here
  url: url,
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
  scripts: [
    {
      src: "/js/commonroom.js",
      async: true,
    },
  ],
  presets: [
    [
      "classic",
      {
        docs: {
          breadcrumbs: false,
          path: "./current_docs",
          routeBasePath: "/",
          sidebarPath: "./sidebars.ts",
          sidebarCollapsible: true,
          editUrl: "https://github.com/dagger/dagger/edit/main/docs",
          remarkPlugins: [
            [
              remarkCodeImport,
              {
                allowImportingFromOutside: true,
              },
            ],
            [remarkTemplate, { version: daggerVersion }],
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
    // Custom webpack configuration for path aliases
    function (context, options) {
      return {
        name: "custom-webpack-config",
        configureWebpack(config, isServer, utils) {
          return {
            resolve: {
              alias: {
                "@cookbookBuild": path.resolve(
                  __dirname,
                  "current_docs/partials/cookbook/builds"
                ),
                "@cookbookFilesystem": path.resolve(
                  __dirname,
                  "current_docs/partials/cookbook/filesystems"
                ),
                "@cookbookContainer": path.resolve(
                  __dirname,
                  "current_docs/partials/cookbook/containers"
                ),
                "@cookbookSecret": path.resolve(
                  __dirname,
                  "current_docs/partials/cookbook/secrets"
                ),
                "@cookbookService": path.resolve(
                  __dirname,
                  "current_docs/partials/cookbook/services"
                ),
                "@cookbookAgent": path.resolve(
                  __dirname,
                  "current_docs/partials/cookbook/agents"
                ),
                "@cookbookError": path.resolve(
                  __dirname,
                  "current_docs/partials/cookbook/errors"
                ),
                "@partials": path.resolve(__dirname, "current_docs/partials"),
                "@daggerTypes": path.resolve(
                  __dirname,
                  "current_docs/partials/types"
                ),
                "@components": path.resolve(__dirname, "src/components"),
              },
            },
          };
        },
      };
    },
    "docusaurus-plugin-sass",
    "docusaurus-plugin-image-zoom",
    // Thanks to @jharrell and Prisma team. Apache-2.0 content
    llmsTxtPlugin,
    [
      "posthog-docusaurus",
      {
        apiKey: "phc_rykA1oJnBnxTwavpgJKr4RAVXEgCkpyPVi21vQ7906d",
        appUrl: "https://us.i.posthog.com", // Changed to standard PostHog URL
        enableInDevelopment: true, // Enable tracking in development
      },
    ],
    [
      "docusaurus-plugin-typedoc",
      {
        id: "current-generation",
        plugin: ["typedoc-plugin-markdown", "typedoc-plugin-frontmatter"],
        entryPoints: [
          "../sdk/typescript/src/connect.ts",
          "../sdk/typescript/src/api/client.gen.ts",
          "../sdk/typescript/src/common/errors/index.ts",
        ],
        tsconfig: "../sdk/typescript/tsconfig.json",
        out: "current_docs/reference/typescript/",
        excludeProtected: true,
        exclude: "../sdk/typescript/node_modules/**",
        skipErrorChecking: true,
        disableSources: true,
        sanitizeComments: true,
        frontmatterGlobals: {
          displayed_sidebar: "current",
          sidebar_label: "TypeScript SDK Reference",
          title: "TypeScript SDK Reference",
        },
        textContentMappings: {
          "title.indexPage": "TypeScript SDK Reference",
          "footer.text": "",
        },
        requiredToBeDocumented: ["Class"],
      },
    ],
  ],
  themes: ["@docusaurus/theme-mermaid"],
  themeConfig: {
    // (jasonmccallister) leaving this in place for future use and reference
    announcementBar: {
      id: "new-docs-published-2025",
      content:
        `We've launched a brand-new docs site! 🎉 Still want the previous one? You can <a href="https://archive.docs.dagger.io/0.18/">find it in our archive</a>.`,
      backgroundColor: "#131126",
      textColor: "#ffffff",
      isCloseable: true,
    },
    sidebar: {
      autoCollapseCategories: false,
      hideable: false,
    },
    docs: {
      sidebar: {
        autoCollapseCategories: false,
        hideable: false,
      },
    },
    metadata: [
      {
        name: "description",
        content:
          "Dagger is an open-source runtime for composable workflows, powering AI agents and CI/CD with modular, repeatable, and observable pipelines.",
      },
      {
        name: "image",
        property: "og:image",
        content: `${url}/img/daggernaut-carpenter-robots-share.jpg`,
      },
      {
        name: "author",
        content: "Dagger",
      },
      {
        property: "twitter:image",
        content: `${url}/img/daggernaut-carpenter-robots-share.jpg`,
      },
    ],
    prism: {
      additionalLanguages: [
        "php",
        "rust",
        "elixir",
        "bash",
        "toml",
        "powershell",
        "java",
      ],
      theme: prismThemes.oneLight,
      darkTheme: prismThemes.oneDark,
    },
    navbar: {
      logo: {
        alt: "Dagger Logo",
        src: "img/dagger-logo-black.png",
        height: "40px",
        href: "https://dagger.io/",
        srcDark: "img/dagger-logo-white.png",
      },
      items: [
        // TODO(jasonmccallister): Add these items back in the nav or possible swizzle into a sidebar or toc?
        // {
        //   position: "right",
        //   href: "https://github.com/dagger/dagger",
        //   html: '<div class="github-stars"><iframe src="https://ghbtns.com/github-btn.html?user=dagger&repo=dagger&type=star&count=true" frameborder="0" scrolling="0" width="120" height="20" title="GitHub Stars"></iframe></div>',
        //   className: "navbar-github-stars",
        // },
        // add the icon and link to join discord
        // {
        //   position: "right",
        //   href: "https://discord.gg/dagger-io",
        //   html: '<div class="discord-icon"><img src="img/discord-icon.svg" alt="Join Discord" /></div>',
        //   className: "navbar-discord-link",
        // },
        {
          position: "right",
          label: "Try Dagger Cloud",
          to: "https://dagger.io/cloud",
          target: "_blank",
          className: "navbar-blog-link dagger-cloud-button",
          id: "dagger-cloud-link",
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
    footer: {
      copyright: `
        <hr />
        <div class="flex justify-between">
          <small>© Dagger 2022-2025</small>
          <div class="flex gap-8">
              <a target="_blank" class="footer-discord-link" href="https://discord.gg/dagger-io">
              </a>
              <a target="_blank" class="footer-x-link" href="https://twitter.com/dagger_io">
              </a>
          </div>
        </div>
      `,
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
