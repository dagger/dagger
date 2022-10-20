/**
 * Creating a sidebar enables you to:
 - create an ordered group of docs
 - render a sidebar for each doc of that group
 - provide next/previous navigation

 The sidebars can be generated from the filesystem, or explicitly defined here.

 Create as many sidebars as you want.
 */

module.exports = {
  'cue-sdk': [
    {
      type: "link",
      label: "Docs Home",
      href: "https://docs.dagger.io/",
    },
    {
      type: "doc",
      label: "Overview",
      id: "index",
    },
    {
      type: "category",
      label: "Getting Started",
      collapsible: false,
      collapsed: false,
      items: [
        "getting-started/install",
        "getting-started/how-it-works",
        {
          type: "category",
          label: "Tutorial",
          items: [
            "getting-started/tutorial/local-dev",
            "getting-started/tutorial/ci-environment",
          ],
        }
      ],
    },
  ],
};
