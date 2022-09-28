/**
 * Creating a sidebar enables you to:
 - create an ordered group of docs
 - render a sidebar for each doc of that group
 - provide next/previous navigation

 The sidebars can be generated from the filesystem, or explicitly defined here.
 e.g. items: [{type: "autogenerated", dirName: "guides"}],

 Create as many sidebars as you want.
 */

module.exports = {
  0.3: [
    {
      type: "category",
      label: "Introduction",
      collapsible: false,
      collapsed: false,
      items: ["unxpq-introduction"],
    },
    {
      type: "doc",
      id: "get-started/bvtz9-get-started"
    },
    {
      type: "category",
      label: "Guides",
      collapsible: false,
      collapsed: false,
      items: [
        "guides/bnzm7-writing_extensions",
        "guides/y0yh0-writing_extensions_go",
        "guides/oy1q7-writing_extensions_nodejs",
        "guides/f5cij-extension_runtime_protocol",
        "guides/d7yxc-operator_manual",
        "guides/joatj-extension_publishing_guide"
      ],
    },
  ],
};
