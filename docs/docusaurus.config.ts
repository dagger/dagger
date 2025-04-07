import fs from "node:fs";
import path from "node:path";
import type { Config } from "@docusaurus/types";
import type * as Preset from "@docusaurus/preset-classic";
import { themes as prismThemes } from "prism-react-renderer";
import remarkCodeImport from "remark-code-import";
import remarkTemplate from "./plugins/remark-template";

import { daggerVersion } from "./current_docs/partials/version";

const url = "https://docs.dagger.io";
const DOCUSAURUS_BASE_URL = process.env.DOCUSAURUS_BASE_URL ?? "/";

const config: Config = {
  title: "Dagger",
  tagline:
    "Open-source runtime for composable workflows, powering AI agents and CI/CD with modular, repeatable, and observable pipelines.",
  favicon: "img/favicon.svg",

  // Set the production url of your site here
  url: url,
  // Set the /<baseUrl>/ pathname under which your site is served
  // For GitHub pages deployment, it is often '/<projectName>/'
  baseUrl: DOCUSAURUS_BASE_URL,

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
          editUrl: "https://github.com/dagger/dagger/edit/main/docs",
          remarkPlugins: [
            [remarkCodeImport, { allowImportingFromOutside: true }],
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
    "docusaurus-plugin-sass",
    "docusaurus-plugin-image-zoom",
    /*
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
    */
    async function pluginLlmsTxt(context) {
      return {
        name: "llms-txt-plugin",
        loadContent: async () => {
          const { siteDir } = context;
          const contentDir = path.join(siteDir, "current_docs");
          const allMdx: string[] = [];

          // recursive function to get all mdx files
          const getMdxFiles = async (dir: string) => {
            const entries = await fs.promises.readdir(dir, { withFileTypes: true });

            for (const entry of entries) {
              const fullPath = path.join(dir, entry.name);
              if (entry.isDirectory()) {
                await getMdxFiles(fullPath);
              } else if (entry.name.endsWith(".mdx")) {
                const content = await fs.promises.readFile(fullPath, "utf8");

                // extract title from frontmatter if it exists
                const titleMatch = content.match(
                  /^---\n(?:.*\n)*?title:\s*["']?([^"'\n]+)["']?\n(?:.*\n)*?---\n/
                );
                const title = titleMatch ? titleMatch[1] : "";

                // Get the relative path for URL construction
                const relativePath = path.relative(contentDir, fullPath);

                // Convert file path to URL path by:
                // 1. Removing numeric prefixes (like 100-, 01-, etc.)
                // 2. Removing the .mdx extension
                let urlPath = relativePath
                  .replace(/^\d+-/, "")
                  .replace(/\/\d+-/g, "/")
                  .replace(/\.mdx$/, "");

                // Construct the full URL
                const fullUrl = `https://www.prisma.io/docs/${urlPath}`;

                // strip frontmatter
                const contentWithoutFrontmatter = content.replace(/^---\n[\s\S]*?\n---\n/, "");

                // combine title and content with URL
                const contentWithTitle = title
                  ? `# ${title}\n\nURL: ${fullUrl}\n${contentWithoutFrontmatter}`
                  : contentWithoutFrontmatter;

                allMdx.push(contentWithTitle);
              }
            }
          };

          await getMdxFiles(contentDir);
          return { allMdx };
        },
        postBuild: async ({ content, routes, outDir }) => {
          const { allMdx } = content as { allMdx: string[] };

          // Write concatenated MDX content
          const concatenatedPath = path.join(outDir, "llms-full.txt");
          await fs.promises.writeFile(concatenatedPath, allMdx.join("\n---\n\n"));

          // we need to dig down several layers:
          // find PluginRouteConfig marked by plugin.name === "docusaurus-plugin-content-docs"
          const docsPluginRouteConfig = routes.filter(
            (route) => route.plugin.name === "docusaurus-plugin-content-docs"
          )[0];

          // docsPluginRouteConfig has a routes property has a record with the path "/" that contains all docs routes.
          const allDocsRouteConfig = docsPluginRouteConfig.routes?.filter(
            (route) => route.path === DOCUSAURUS_BASE_URL
          )[0];

          // A little type checking first
          if (!allDocsRouteConfig?.props?.version) {
            return;
          }

          // this route config has a `props` property that contains the current documentation.
          const currentVersionDocsRoutes = (
            allDocsRouteConfig.props.version as Record<string, unknown>
          ).docs as Record<string, Record<string, unknown>>;

          // for every single docs route we now parse a path (which is the key) and a title
          const docsRecords = Object.entries(currentVersionDocsRoutes).map(([path, record]) => {
            return `- [${record.title}](${path}): ${record.description}`;
          });

          // Build up llms.txt file
          const llmsTxt = `# ${context.siteConfig.title}\n\n## Docs\n\n${docsRecords.join("\n")}`;

          // Write llms.txt file
          const llmsTxtPath = path.join(outDir, "llms.txt");
          await fs.promises.writeFile(llmsTxtPath, llmsTxt);
        },
      };
    },
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
    sidebarCollapsed: false,
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
      theme: prismThemes.dracula,
    },
    navbar: {
      logo: {
        alt: "Dagger Logo",
        src: "img/dagger-logo-white.svg",
        height: "50px",
        href: "https://dagger.io/",
      },
      items: [
        {
          position: "left",
          to: "https://daggerverse.dev/",
          label: "Daggerverse",
          className: "navbar-blog-link",
          target: "_self",
        },
        {
          position: "left",
          to: "/",
          label: "Docs",
          className: "navbar-blog-link",
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
      links: [
        {
          title: "Resources",
          items: [
            {
              label: "Case Studies",
              to: "https://dagger.io/case-studies",
            },
            {
              label: "Videos",
              to: "https://dagger.io/videos",
            },
            {
              label: "Adopting Dagger",
              to: "https://dagger.io/adopting-dagger",
            },
            {
              label: "Daggerized Projects",
              to: "https://dagger.io/daggerized-projects",
            },
            {
              label: "Docs",
              to: "https://docs.dagger.io/",
            },
            {
              label: "Blog",
              to: "https://dagger.io/blog",
            },
            {
              label: "Community Content",
              to: "https://dagger.io/community-content",
            },
          ],
        },
        {
          title: "Community",
          items: [
            {
              label: "Events",
              to: "https://dagger.io/events",
            },
            {
              label: "Get Involved",
              to: "https://dagger.io/community",
            },
            {
              label: "Dagger Love",
              to: "https://dagger.io/dagger-love",
            },
            {
              label: "Dagger Commanders",
              to: "https://dagger.io/commanders",
            },
          ],
        },
        {
          title: "Product",
          items: [
            {
              label: "Dagger Engine",
              to: "https://dagger.io/dagger-engine",
            },
            {
              label: "Dagger Cloud",
              to: "https://dagger.io/cloud",
            },
            {
              label: "Daggerverse",
              to: "https://daggerverse.dev",
            },
            {
              label: "Integrations",
              to: "https://dagger.io/integrations",
            },
            {
              label: "Pricing",
              to: "https://dagger.io/pricing",
            },
          ],
        },
        {
          title: "Company",
          items: [
            {
              label: "Partners",
              to: "https://dagger.io/partners",
            },
            {
              label: "Careers",
              to: "https://boards.greenhouse.io/dagger",
            },
            {
              label: "Brand",
              to: "https://dagger.io/brand",
            },
            {
              label: "Terms of Service",
              to: "https://dagger.io/terms-of-service",
            },
            {
              label: "Privacy Policy",
              to: "https://dagger.io/privacy-policy",
            },
            {
              label: "Trademark Guidelines",
              to: "https://dagger.io/trademark-guidelines",
            },
            {
              label: "Dagger Trust Center",
              to: "https://trust.dagger.io",
            },
          ],
        },
      ],
      copyright: `
        <hr />
        <div class="flex justify-between">
          <small>© Dagger 2022-2024</small>
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
