# Archiving Documentation

Current and next versions of documentation live at [https://docs.dagger.io](https://docs.dagger.io).

All previous versions of documentation are stored in the Dagger documentation archive at [https://archive.docs.dagger.io](https://archive.docs.dagger.io).

[https://archive.docs.dagger.io](https://archive.docs.dagger.io) is a statically-built site, with individual sub-sites for each documentation version and a single index page to direct users.

This document explains how to build the top-level site and the individual sub-sites.

NOTE: At the time of writing, this is a completely manual process. This is because we expect to rebuild this archive only infrequently (at most, once or twice per year) and therefore investing in an automation script seems unnecessary.

## Build 0.1 sub-site

- Clone branch `v0.1.0` at last commit
- In `docusaurus.config.js`:
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

- In `docs/` sub-directory:
  - Replace `/img` paths with `/0.1/img` paths
- Run `npm run build:withoutAuth` and store the `build/` directory as `site/0.1`

## Build 0.2 sub-site

- Clone branch `v0.2.x` at last commit
- Delete `v0.1/` sub-directory
- In `sidebars.js`:
  - Delete `0.1` sidebar entry
  - Delete 0.2 link in `0.1` sidebar list

        {
          type: "link",
          label: "⬅️ Dagger 0.1",
          href: "/0.1",
        },

- In `docusaurus.config.js`:
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

- In `docs/v0.2` sub-directory:
  - Replace `/img` paths with `/0.2/img` paths in React component code in `dgr18-overview.mdx`
  - Replace `/img` paths with `/0.2/img` paths in React component code in `getting-started/1242-install.mdx`
  - Replace `/img` paths with `/0.2/img` paths in `getting-started/f44rm-how-it-works.mdx`
- Run `npm run build` and store the `build/` directory as `site/0.2`

## Build top-level site (archive.docs.dagger.io)

- Obtain the index page template from `archived_docs/index.html.tmpl` and modify as needed.
- Create and upload this filesystem structure to the Netlify site

      site/
        0.1/
        0.2/
        index.html
