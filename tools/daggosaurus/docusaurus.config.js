const path = require('path');

/** @type {import('@docusaurus/types').DocusaurusConfig} */
module.exports = {
  title: 'Dagger',
  tagline: 'Dagger is a programmable deployment system',
  url: 'https://docs.dagger.io',
  baseUrl: '/',
  onBrokenLinks: 'throw',
  onBrokenMarkdownLinks: 'warn',
  favicon: 'img/favicon.png',
  organizationName: 'Dagger',
  projectName: 'Dagger',
  stylesheets: [
    'https://fonts.gstatic.com',
    'https://fonts.googleapis.com/css2?family=Poppins:wght@700&display=swap',
    'https://fonts.googleapis.com/css2?family=Karla&family=Poppins:wght@700&display=swap'
  ],
  themeConfig: {
    sidebarCollapsible: false,
    prism: {
      defaultLanguage: 'go',
    },
    navbar: {
      logo: {
        alt: 'Dagger Logo',
        src: 'img/dagger-logo.png',
        srcDark: 'img/dagger_logo_dark.png',
      },
    },
    algolia: {
      apiKey: 'cd4551565ea091140ab8f6c968ea670f',
      indexName: 'docs_dagger'
    },
    colorMode: {
      // "light" | "dark"
      defaultMode: 'light',

      switchConfig: {
        darkIcon: "img/Icon_Night-mode.svg",
        lightIcon: 'img/Icon_Day-mode.svg',
      },
    },
  },
  presets: [
    [
      '@docusaurus/preset-classic',
      {
        docs: {
          sidebarPath: require.resolve('./sidebars.js'),
          editUrl:
            'https://github.com/dagger/dagger/blob/main',
          routeBasePath: '/',
        },
        theme: {
          customCss: require.resolve('./src/css/custom.css'),
        },
      },
    ],
  ],
  plugins: [path.resolve(__dirname, './custom_plugins')],
};