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
        alt: 'My Site Logo',
        src: 'img/dagger-logo.png',
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