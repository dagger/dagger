const path = require("path");

/** @type {import('@docusaurus/types').DocusaurusConfig} */
module.exports = {
  title: "Dagger",
  tagline: "Dagger is a programmable deployment system",
  url: "https://docs.dagger.io",
  baseUrl: "/",
  onBrokenLinks: "warn",
  onBrokenMarkdownLinks: "warn",
  favicon: "img/favicon.png",
  organizationName: "Dagger",
  projectName: "Dagger",
  stylesheets: [
    "https://fonts.googleapis.com/css2?family=Karla&family=Poppins:wght@700&display=swap",
  ],
  themeConfig: {
    sidebarCollapsible: true,
    prism: {
      theme: require("prism-react-renderer/themes/okaidia"),
    },
    navbar: {
      logo: {
        alt: "Dagger Logo",
        src: "img/dagger-logo.png",
        srcDark: "img/dagger_logo_dark.png",
      },
      items: [
        {
          label: "Github",
          href: "https://github.com/dagger/dagger",
          position: "right",
        },
        {
          label: "Discord",
          href: "https://discord.gg/ufnyBtc8uY",
          position: "right",
        },
        {
          label: "Schedule a Demo",
          href: "https://calendly.com/dagger-io/meet-the-dagger-team",
          position: "right",
        },
      ],
    },
    algolia: {
      apiKey: "b2324f1ac8932ab80916382521473115",
      indexName: "daggosaurus",
    },
    colorMode: {
      // "light" | "dark"
      defaultMode: "light",

      switchConfig: {
        darkIcon: "img/Icon_Night-mode.svg",
        lightIcon: "img/Icon_Day-mode.svg",
      },
    },
    gtag: {
      trackingID: "G-RDXG80F635",
      anonymizeIP: true,
    },
  },
  presets: [
    [
      "@docusaurus/preset-classic",
      {
        docs: {
          path: "../docs",
          sidebarPath: require.resolve("./sidebars.js"),
          editUrl: "https://github.com/dagger/dagger/blob/main",
          routeBasePath: "/",
        },
        theme: {
          customCss: require.resolve("./src/css/custom.scss"),
        },
      },
    ],
  ],
  plugins: [
    "docusaurus-plugin-sass",
    [
      "docusaurus2-dotenv",
      {
        systemvars: true,
        expand: true,
      },
    ],
  ],
};
