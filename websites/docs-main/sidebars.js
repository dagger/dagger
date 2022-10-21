/**
 * Creating a sidebar enables you to:
 - create an ordered group of docs
 - render a sidebar for each doc of that group
 - provide next/previous navigation

 The sidebars can be generated from the filesystem, or explicitly defined here.

 Create as many sidebars as you want.
 */

module.exports = {
  'main': [
    {
      type: "doc",
      id: "overview",
    },
    {
      type: "link",
      label: "Go SDK",
      href: "https://docs-v03x.dagger.io/sdk/go"
    },
    {
      type: "link",
      label: "CUE SDK",
      href: "https://docs-v03x.dagger.io/sdk/cue"
    },
    {
      type: "doc",
      id: "faq"
    },
  ],
};
