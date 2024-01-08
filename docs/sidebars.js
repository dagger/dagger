/**
 * Creating a sidebar enables you to:
 - create an ordered group of docs
 - render a sidebar for each doc of that group
 - provide next/previous navigation

 The sidebars can be generated from the filesystem, or explicitly defined here.

 Create as many sidebars as you want.
 */

module.exports = {
  current: [
    {
      type: "doc",
      id: "index",
      label: "Introduction",
    },
    {
      type: "doc",
      id: "quickstart/index",
      label: "Quickstart",
    },
    {
      type: "doc",
      id: "guides",
      label: "Guides",
    },
    {
      type: "doc",
      id: "cookbook",
      label: "Cookbook",
    },
    {
      type: "category",
      label: "Dagger Cloud",
      collapsible: true,
      collapsed: true,
      items: [
        {
          type: "doc",
          label: "Overview",
          id: "cloud/index",
        },
        {
          type: "doc",
          label: "Get Started",
          id: "cloud/get-started",
        },
        {
          type: "category",
          label: "Reference",
          collapsible: true,
          collapsed: true,
          items: [
            "cloud/reference/user-interface",
            "cloud/reference/roles-permissions",
            "cloud/reference/org-administration",
          ]
        },
      ],
    },
    {
      type: "category",
      label: "Dagger SDKs and API",
      collapsible: true,
      collapsed: true,
      items: [
        {
          type: "category",
          label: "Go SDK",
          collapsible: true,
          collapsed: true,
          items: [
            {
              type: "doc",
              label: "Overview",
              id: "sdk/go/index",
            },
            "sdk/go/install",
            {
              type: "doc",
              label: "Get Started",
              id: "sdk/go/get-started",
            },
            {
              type: "link",
              label: "Reference",
              href: "https://pkg.go.dev/dagger.io/dagger",
            },
          ],
        },
        {
          type: "category",
          label: "TypeScript SDK",
          collapsible: true,
          collapsed: true,
          items: [
            {
              type: "doc",
              label: "Overview",
              id: "sdk/typescript/index",
            },
            "sdk/typescript/install",
            {
              type: "doc",
              label: "Get Started",
              id: "sdk/typescript/get-started",
            },
            {
              type: "doc",
              label: "Reference",
              id: "sdk/typescript/reference/modules",
            },
          ],
        },
        {
          type: "category",
          label: "Python SDK",
          collapsible: true,
          collapsed: true,
          items: [
            {
              type: "doc",
              label: "Overview",
              id: "sdk/python/index",
            },
            "sdk/python/install",
            {
              type: "doc",
              label: "Get Started",
              id: "sdk/python/get-started",
            },
            {
              type: "link",
              label: "Reference",
              href: "https://dagger-io.readthedocs.org/",
            },
          ],
        },
        {
          type: "category",
          label: "Elixir SDK (Experimental)",
          collapsible: true,
          collapsed: true,
          items: [
            {
              type: "doc",
              label: "Overview",
              id: "sdk/elixir/index",
            },
            "sdk/elixir/install",
            {
              type: "doc",
              label: "Get Started",
              id: "sdk/elixir/get-started",
            },
            {
              type: "link",
              label: "Reference",
              href: "https://hexdocs.pm/dagger/Dagger.html",
            },
          ],
        },
        {
          type: "category",
          label: "GraphQL API",
          collapsible: true,
          collapsed: true,
          items: [
            {
              type: "doc",
              label: "Overview",
              id: "api/index",
            },
            "api/concepts",
            "api/playground",
            "api/build-custom-client",
            {
              type: "link",
              label: "Reference",
              href: "https://docs.dagger.io/api/reference",
            },
          ],
        },
      ]
    },
    {
      type: "category",
      label: "Dagger CLI",
      collapsible: true,
      collapsed: true,
      items: [
        {
          type: "doc",
          label: "Overview",
          id: "cli/index",
        },
        "cli/install",
        "cli/run-pipelines-cli",
        {
          type: "doc",
          label: "Reference",
          id: "cli/reference",
        },
      ],
    },
    {
      type: "doc",
      id: "faq",
    },
    {
      type: "doc",
      id: "contributing",
    },
    {
      type: "link",
      label: "Changelog",
      href: "https://github.com/dagger/dagger/blob/main/CHANGELOG.md",
    },
  ],
  quickstart: [
    {
      type: "doc",
      id: "index",
      label: "Home",
    },
    {
      type: "category",
      label: "Quickstart",
      collapsible: false,
      collapsed: false,
      items: [
        "quickstart/index",
        "quickstart/basics",
        "quickstart/setup",
        "quickstart/cli",
        "quickstart/sdk",
        "quickstart/hello",
        "quickstart/test",
        "quickstart/build",
        "quickstart/publish",
        "quickstart/build-multi",
        "quickstart/caching",
        "quickstart/build-dockerfile",
        "quickstart/conclusion",
      ]
    }
  ],

};
