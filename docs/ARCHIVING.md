# Archiving Documentation

Current and next versions of documentation live at [https://docs.dagger.io](https://docs.dagger.io).

All previous versions of documentation are stored in the Dagger documentation archive at [https://archive.docs.dagger.io](https://archive.docs.dagger.io).

[https://archive.docs.dagger.io](https://archive.docs.dagger.io) is a statically-built site, with individual sub-sites for each documentation version and a single index page to direct users.

This document explains how to build the top-level site and the individual sub-sites.

NOTE: At the time of writing, this is a completely manual process. This is because we expect to rebuild this archive only infrequently (at most, once or twice per year) and therefore investing in an automation script seems unnecessary.

## Build 0.1 sub-site

- Clone branch `v0.1.0` at last commit eebd918
- In `docusaurus.config.js`:
  - Set `url: 'https://archive.docs.dagger.io'`
  - Set `baseUrl: "/0.1/"`
  - Add announcement bar in `themeConfig` object

        themeConfig: {
          //
          announcementBar: {
            id: 'unmaintained_docs',
            content:
              'This is the documentation for Dagger 0.1.x, which is no longer maintained. We encourage you to upgrade. For up-to-date documentation, visit <a target="_blank" rel="noopener noreferrer" href="https://docs.dagger.io">docs.dagger.io</a>.',
            backgroundColor: '#fcc009',
            textColor: '#000000',
            isCloseable: false,
          },
        }

  - Delete search bar

        {
          type: "search",
          position: "right",
          className: "header-searchbar",
        },

  - Delete Algolia search config

        algolia: {
          apiKey: "bffda1490c07dcce81a26a144115cc02",
          indexName: "dagger",
          appId: "XEIYPBWGOI",
        },

  - Delete edit URL

        editUrl: "https://github.com/dagger/dagger/edit/main/website",

  - Add noindex metadata in `themeConfig` object

        themeConfig: {
          metadata: [
            { name: 'robots', content: 'noindex, nofollow' },
          ],
        }

- In `docs/` sub-directory:
  - Replace `/img` paths with `/0.1/img` paths
- Run `npm run build:withoutAuth` and store the `build/` directory as `site/0.1`

## Build 0.2 sub-site

- Clone branch `v0.2.36` at last commit ea275a3
- In `sidebars.js`:
  - Delete `0.1` sidebar entry
  - Delete 0.1 link in `0.2` sidebar list

        {
          type: "link",
          label: "⬅️ Dagger 0.1",
          href: "/0.1",
        },

- In `docusaurus.config.js`:
  - Set `url: 'https://archive.docs.dagger.io'`
  - Set `baseUrl: "/0.2/"`
  - Add announcement bar in `themeConfig` object

        themeConfig: {
          //
          announcementBar: {
            id: 'unmaintained_docs',
            content:
              'This is the documentation for Dagger 0.2.x, which is no longer maintained. We encourage you to upgrade. For up-to-date documentation, visit <a target="_blank" rel="noopener noreferrer" href="https://docs.dagger.io">docs.dagger.io</a>.',
            backgroundColor: '#fcc009',
            textColor: '#000000',
            isCloseable: false,
          },
        }

  - Add noindex metadata in `themeConfig` object

        themeConfig: {
          metadata: [
            { name: 'robots', content: 'noindex, nofollow' },
          ]
        }

  - Delete search bar

        {
          type: "search",
          position: "right",
          className: "header-searchbar",
        },

  - Delete Algolia search config

        algolia: {
          apiKey: "bffda1490c07dcce81a26a144115cc02",
          indexName: "dagger",
          appId: "XEIYPBWGOI",
        },

  - Delete Hotjar config

        hotjar: {
          siteId: "2541514",
        },

  - Delete edit URL

          editUrl: "https://github.com/dagger/dagger/edit/main/website",

- In `docs/` sub-directory:
  - Replace `/img` path with `/0.2/img` paths in `dgr18-overview.mdx`
  - Replace `/img` path with `/0.2/img` paths in `getting-started/f44rm-how-it-works.mdx`
  - Replace `/img` path with `/0.2/img` paths in `getting-started/1242-install.mdx`
  - Delete all instances of `displayed_sidebar: '0.1'`
- Replace `/img` path with `/0.2/img` paths in `website/src/components/atoms/linkCTA.js`
- Run `npm run build` and store the `build/` directory as `site/0.2`

## Build 0.9 sub-site

- Copy the following to a temporary location (`temp`) from the `main` branch before beginning
  - `docs\archived_docs\assets`
  - `docs\src\scripts\replace-embeds.go`
- Clone branch `v0.9.x` at last commit 77a53a8
- Delete `docs/versioned_docs/version-zenith` sub-directory
- Delete `docs/versioned_sidebars/version-zenith-sidebars.json` file
- Remove `zenith` entry from `docs/versions.json`

- In `docusaurus.config.js`:
  - Set `url: 'https://archive.docs.dagger.io'`
  - Set `baseUrl: "/0.9/"`
  - Add announcement bar in `themeConfig` object

        themeConfig: {
          //
          announcementBar: {
            id: 'unmaintained_docs',
            content:
              'This is the documentation for Dagger 0.9.x, which is no longer maintained. We encourage you to upgrade. For up-to-date documentation, visit <a target="_blank" rel="noopener noreferrer" href="https://docs.dagger.io">docs.dagger.io</a>.',
            backgroundColor: '#fcc009',
            textColor: '#000000',
            isCloseable: false,
          },
        }

  - Delete search bar

        {
          type: "search",
          position: "right",
          className: "header-searchbar",
        },

  - Delete Algolia search config

        algolia: {
          apiKey: "bffda1490c07dcce81a26a144115cc02",
          indexName: "dagger",
          appId: "XEIYPBWGOI",
        },

  - Delete edit URL

          editUrl: "https://github.com/dagger/dagger/edit/main/website",

  - Delete entire `versions` object

        versions: {
          zenith: {
            path: '/zenith',
            banner: 'none',
            badge: false
          },
          current: {
            path: '/',
            banner: 'none',
            badge: false
          },
        },

  - Delete `versionedGuidesPath` from `plugins` object

        versionedGuidesPath: "./versioned_docs/version-zenith/guides"

  - Delete `zenith-generation` reference doc entry from `plugins` object

        [
          "docusaurus-plugin-typedoc",
          {
            id: "zenith-generation",
            entryPoints: ['../sdk/typescript/connect.ts', '../sdk/typescript/api/client.gen.ts', '../sdk/typescript/common/errors/index.ts'],
            tsconfig: '../sdk/typescript/tsconfig.json',
            // Zenith reference
            out: '../versioned_docs/version-zenith/reference/typescript/',
            excludeProtected: true,
            exclude: '../sdk/typescript/node_modules/**',
            skipErrorChecking: true,
            disableSources: true,
            sidebar: {
              categoryLabel: 'TypeScript SDK Reference',
            },
            frontmatter: {
              displayed_sidebar: 'zenith',
              sidebar_label: 'TypeScript SDK Reference',
              title: "TypeScript SDK Reference"
            },
            hideMembersSymbol: true,
            requiredToBeDocumented: ["Class"]
          },
        ],

  - Add noindex metadata in `themeConfig` object

        metadata: [
          { name: 'robots', content: 'noindex, nofollow' },
        ]

- In `docs/current_docs/` sub-directory:
  - Replace `(/cli` URL links with `(/0.9/cli` URL links
  - Replace `(/sdk` URL links with `(/0.9/sdk` URL links
- In `docs/current_docs/guides/` sub-directory:
  - Replace `(../sdk/nodejs/)` URL links with `(/0.9/sdk/nodejs/)` URL links
  - Replace `(../sdk/python/)` URL links with `(/0.9/sdk/python/)` URL links
  - Replace `(../sdk/go/)` URL links with `(/0.9/sdk/go/)` URL links
  - Replace `(../api/)` URL links with `(/0.9/api/)` URL links
- Replace embedded snippets with file references
  - Remove `import QuickstartDoc from '@site/src/components/quickstartDoc.js'` in all pages
  - Remove `<QuickstartDoc embeds={ids}>` in all pages
  - Remove `</QuickstartDoc>` in all pages
  - Move `temp/assets/0.9/api/snippets/*` to `docs/current_docs/api/snippets` (snippets which previously existed only as embeds, not files)
  - Move `temp/assets/0.9/quickstart/snippets/*` to `docs/current_docs/quickstart/snippets` (snippets which previously existed only as embeds, not files)
  - From the `docs` directory, run the command `go run src/scripts/replace-embeds.go` to replace all embedded snippets with file references
  - Perform a search over the `docs` directory tree to confirm that there are no orphaned `<Embed` directives
- Run `npm run build` and store the `build/` directory as `site/0.9`

## Build top-level site (archive.docs.dagger.io)

- Obtain the index page template from `archived_docs/index.html.tmpl` and modify as needed.
- Copy the `archived_docs/netlify.toml` file to the site root (needed for the embargoes and the API Playground embeds in the 0.9/ sub-site)
- Create and upload this filesystem structure to the Netlify site

      site/
        0.1/
        0.2/
        0.9/
        index.html
        netlify.toml
