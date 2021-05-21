module.exports = {
  pathPrefix: `/${process.env.VERSION}`,
  siteMetadata: {
    siteTitle: `Dagger Docs`,
    defaultTitle: `Dagger Docs`,
    siteTitleShort: `Dagger Docs`,
    siteDescription: `Dagger Documentation`,
    siteUrl: `https://launch.dagger.io`,
    siteAuthor: `@dagger`,
    siteImage: `/banner.png`,
    siteLanguage: `en`,
    themeColor: `#1890FF`,
    docVersion : `${process.env.VERSION}`,
  },
  flags: { PRESERVE_WEBPACK_CACHE: true },
  plugins: [
    {
      resolve: `@rocketseat/gatsby-theme-docs`,
      options: {
        basePath: `/`,
        configPath: `docs/sidebar`,
        docsPath: `docs`,
        repositoryUrl: `https://github.com/dagger/dagger`,
        baseDir: `/`,
      },
    },
  ],
};
