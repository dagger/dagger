/**
 * Creating a sidebar enables you to:
 - create an ordered group of docs
 - render a sidebar for each doc of that group
 - provide next/previous navigation

 The sidebars can be generated from the filesystem, or explicitly defined here.

 Create as many sidebars as you want.
 */

module.exports = {
  'go-sdk': [
    {
      type: "link",
      label: "Docs Home",
      href: "/",
    },
    {
      type: "doc",
      label: "Overview",
      id: "fssrz-index"
    },
    {
      type: "doc",
      id: "r2eu9-install",
    },
    {
      type: "doc",
      label: "Get Started",
      id: "8g34z-get-started",
    },
    {
      type: "link",
      label: "Reference",
      href: "https://pkg.go.dev/go.dagger.io/dagger@v0.3.0-alpha.1"
    }

  ],
};
