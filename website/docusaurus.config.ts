import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';
import * as path from 'path';

const config: Config = {
  title: 'Dagger',
  tagline: 'CI/CD as Code',
  favicon: 'img/favicon.png',

  // Set the production url of your site here
  url: 'https://docs.dagger.io',
  // Set the /<baseUrl>/ pathname under which your site is served
  // For GitHub pages deployment, it is often '/<projectName>/'
  baseUrl: '/',

  // GitHub pages deployment config.
  // If you aren't using GitHub pages, you don't need these.
  //organizationName: 'Dagger', // Usually your GitHub org/user name.
  //projectName: 'Dagger', // Usually your repo name.

  onBrokenLinks: 'throw',
  onBrokenMarkdownLinks: 'throw',

  // Even if you don't use internationalization, you can use this field to set
  // useful metadata like html lang. For example, if your site is Chinese, you
  // may want to replace "en" with "zh-Hans".
  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },
  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
          editUrl:
            'https://github.com/dagger/dagger/edit/main/website',
        },
        blog: false,
        theme: {
          customCss: require.resolve("./src/css/custom.scss"),
        },
        gtag: {
          trackingID: "G-RDXG80F635",
          anonymizeIP: true,
        },
      } satisfies Preset.Options,
    ],
  ],
  plugins: [
    "docusaurus-plugin-sass",
    path.resolve(__dirname, "plugins/docusaurus-plugin-hotjar"),
    path.resolve(__dirname, "plugins/docusaurus-plugin-guides"),
    path.resolve(__dirname, "plugins/docusaurus-plugin-dagger-version"),
    [
      "posthog-docusaurus",
      {
        appUrl: "https://dagger.io/analytics",
        apiKey: "phc_rykA1oJnBnxTwavpgJKr4RAVXEgCkpyPVi21vQ7906d"
      }
    ],
  ],
  themeConfig: {
    sidebarCollapsed: false,
    metadata: [{ name: 'og:image', content: '/img/favicon.png' }],
    prism: {
      additionalLanguages: ["php", "rust", "elixir"],
      theme: prismThemes.dracula,
    },
    navbar: {
      logo: {
        alt: "Dagger Logo",
        src: "img/dagger-logo-white.svg",
        height: "50px",
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
  } satisfies Preset.ThemeConfig,
};

export default config;
