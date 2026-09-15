import type { Config } from "@docusaurus/types";
import type * as Preset from "@docusaurus/preset-classic";
import remarkCodeImport from "remark-code-import";
import remarkTemplate from "./plugins/remark-template";
import llmsTxtPlugin from "./plugins/llms-txt-plugin";
import daggerApiReference from "./plugins/dagger-api-reference";
import path from "path";
import { daggerPrismTheme } from "./src/prism/theme";

import { daggerVersion } from "./current_docs/partials/version";
import versions from "./versions.json";

const url = "https://docs.dagger.io";
const docsPath = "./current_docs";
const baseUrl = process.env.DOCUSAURUS_BASE_URL ?? "/";
// General Sans and Inter are self-hosted under static/fonts and declared with
// @font-face in custom.scss; preloading the three faces used above the fold
// keeps them from swapping in late. Source Code Pro comes from Google Fonts,
// as it does on dagger.io.
function daggerWebFontsPlugin() {
  return {
    name: "dagger-webfonts",
    injectHtmlTags() {
      const preload = (file: string) => ({
        tagName: "link",
        attributes: {
          rel: "preload",
          href: `${baseUrl}fonts/${file}`,
          as: "font",
          type: "font/woff2",
          crossorigin: "anonymous",
        },
      });

      return {
        headTags: [
          preload("general-sans-semibold.woff2"),
          preload("general-sans-medium.woff2"),
          preload("inter-400.woff2"),
          {
            tagName: "link",
            attributes: {
              rel: "preconnect",
              href: "https://fonts.googleapis.com",
            },
          },
          {
            tagName: "link",
            attributes: {
              rel: "preconnect",
              href: "https://fonts.gstatic.com",
              crossorigin: "anonymous",
            },
          },
          {
            tagName: "link",
            attributes: {
              rel: "stylesheet",
              href: "https://fonts.googleapis.com/css2?family=Source+Code+Pro:wght@400;500&display=swap",
            },
          },
        ],
      };
    },
  };
}

const config: Config = {
  title: "Dagger",
  tagline:
    "Dagger is a CI orchestration engine. Define your pipelines once, in real code. Run them identically before and after push, locally or at scale.",
  favicon: "img/favicon.svg",

  // Set the production url of your site here
  url: url,
  // Set the /<baseUrl>/ pathname under which your site is served
  // For GitHub pages deployment, it is often '/<projectName>/'
  baseUrl,

  future: {
    experimental_faster: {
      swcJsLoader: true,
      swcJsMinimizer: true,
      swcHtmlMinimizer: false,
      lightningCssMinimizer: true,
      mdxCrossCompilerCache: true,
      rspackBundler: true,
      rspackPersistentCache: true,
    },
  },

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
      src: `${baseUrl}js/commonroom.js`,
      async: true,
    },
  ],
  presets: [
    [
      "classic",
      {
        docs: {
          breadcrumbs: false,
          path: docsPath,
          routeBasePath: "/",
          // No lastVersion: the default (served at /) is the newest snapshot,
          // i.e. versions.json[0], which docs:version prepends on each cut.
          versions: {
            // Only the default version (versions.json[0], served at /) should
            // be indexed by search engines; archived snapshots and the
            // unreleased docs otherwise compete with it in search results.
            ...Object.fromEntries(
              versions.slice(1).map((v) => [v, { noIndex: true }]),
            ),
            current: {
              label: "Next",
              path: "next",
              banner: "unreleased",
              badge: false,
              noIndex: true,
            },
          },
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
    daggerWebFontsPlugin,
    // Custom webpack configuration for path aliases
    function(context, options) {
      return {
        name: "custom-webpack-config",
        configureWebpack(config, isServer, utils) {
          return {
            resolve: {
              alias: {
                "@components": path.resolve(__dirname, "src/components"),
                "@daggerTypes": path.resolve(
                  __dirname,
                  "current_docs/partials/types",
                ),
              },
            },
          };
        },
      };
    },
    "docusaurus-plugin-sass",
    "docusaurus-plugin-image-zoom",
    // Thanks to @jharrell and Prisma team. Apache-2.0 content
    [llmsTxtPlugin, { docsPath }],
    // Parses docs-graphql/schema.graphqls into the model rendered by the
    // API reference components on the type reference pages.
    daggerApiReference,
    // Builds a client-side search index over the current docs version. Pairs
    // with the swizzled SearchBar (src/theme/SearchBar) for a local,
    // command-palette search that needs no external service. Search covers the
    // latest version (served at the root) and the unreleased /next docs; older
    // snapshots live under a /<version>/ prefix and are skipped by the plugin.
    // The generated SDK reference is excluded as noise.
    ["./plugins/local-search", { exclude: ["/reference/typescript/"] }],
    [
      "posthog-docusaurus",
      {
        apiKey: "phc_rykA1oJnBnxTwavpgJKr4RAVXEgCkpyPVi21vQ7906d",
        appUrl: "https://us.i.posthog.com", // Changed to standard PostHog URL
        enableInDevelopment: true, // Enable tracking in development
      },
    ],
  ],
  themes: ["@docusaurus/theme-mermaid"],
  themeConfig: {
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
      // One theme for both modes: its colours are CSS variables that flip
      // with html[data-theme]. See src/prism/theme.ts.
      theme: daggerPrismTheme,
      darkTheme: daggerPrismTheme,
    },
    navbar: {
      // The website's masthead sets DAGGER as type, not as an image. The
      // logo entry stays so Docusaurus keeps the link target, but custom.scss
      // hides the image and renders the wordmark from `title`.
      title: "DAGGER",
      logo: {
        alt: "Dagger",
        src: "img/dagger-logo-black.png",
        href: "https://dagger.io/",
        srcDark: "img/dagger-logo-white.png",
      },
      items: [
        {
          type: "custom-docsVersionSelect",
          position: "right",
          className: "navbar-version-select-mobile",
        },
        {
          type: "docsVersionDropdown",
          position: "right",
          className: "navbar-version-dropdown",
        },
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
        // The masthead's auth pair, copied from dagger.io/src/data/nav.ts.
        // It reads as one bracketed item but each half is its own link. Both
        // hrefs point at the app root on purpose: /login fires OAuth and
        // /signup is post-authentication onboarding, so an anonymous visitor
        // sent there bounces back to / anyway. Kept as two entries so each
        // gets its own href once the cloud app grows a signup route.
        {
          type: "html",
          position: "right",
          className: "navbar-auth",
          value:
            '<span class="navbar__auth">[ <a href="https://dagger.cloud">LOG IN</a> / <a href="https://dagger.cloud">SIGN UP</a> ]</span>',
        },
        {
          type: "search",
          position: "right",
          className: "header-searchbar",
        },
      ],
    },
    // Follow the OS theme with no toggle, matching dagger.io. Docusaurus's
    // inline script still sets html[data-theme] before paint, so there is no
    // flash of the wrong theme.
    colorMode: {
      defaultMode: "light",
      disableSwitch: true,
      respectPrefersColorScheme: true,
    },
    zoom: {
      selector: ".markdown img:not(.not-zoom)",
      background: {
        light: "#f8f4ef",
        dark: "#0d0c1b",
      },
      // medium-zoom configuration options
      // Refer to https://github.com/francoischalifour/medium-zoom#options
      config: {},
    },
    // The website's colophon: a hairline rule, the wordmark and year on the
    // left, mono links on the right. Rendered through `copyright` because it
    // is a single row, not Docusaurus's multi-column link grid.
    footer: {
      copyright: `
        <div class="colophon__inner">
          <div>DAGGER &mdash; ${new Date().getFullYear()}</div>
          <div class="colophon__links">
            <a href="https://github.com/dagger/dagger">GITHUB</a>
            <a href="https://discord.gg/dagger-io">DISCORD</a>
            <a href="https://x.com/dagger_io">X</a>
            <a href="https://www.youtube.com/@dagger-io">YOUTUBE</a>
            <a href="https://dagger.io/legal_pages/privacy-policy">PRIVACY</a>
            <a href="https://dagger.io/legal_pages/terms-of-service">TERMS</a>
          </div>
        </div>
      `,
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
