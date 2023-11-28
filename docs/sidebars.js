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
      id: "current/index",
      label: "Introduction",
    },
    {
      type: "doc",
      id: "current/quickstart/index",
      label: "Quickstart",
    },
    {
      type: "doc",
      id: "current/guides",
      label: "Guides",
    },
    {
      type: "doc",
      id: "current/cookbook",
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
          id: "current/cloud/index",
        },
        {
          type: "doc",
          label: "Get Started",
          id: "current/cloud/get-started",
        },
        {
          type: "category",
          label: "Reference",
          collapsible: true,
          collapsed: true,
          items: [
            "current/cloud/reference/user-interface",
            "current/cloud/reference/roles-permissions",
            "current/cloud/reference/org-administration",
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
              id: "current/sdk/go/index",
            },
            "current/sdk/go/install",
            {
              type: "doc",
              label: "Get Started",
              id: "current/sdk/go/get-started",
            },
            {
              type: "link",
              label: "Reference 🔗",
              href: "https://pkg.go.dev/dagger.io/dagger",
            },
          ],
        },
        {
          type: "category",
          label: "Node.js SDK",
          collapsible: true,
          collapsed: true,
          items: [
            {
              type: "doc",
              label: "Overview",
              id: "current/sdk/nodejs/index",
            },
            "current/sdk/nodejs/install",
            {
              type: "doc",
              label: "Get Started",
              id: "current/sdk/nodejs/get-started",
            },
            {
              type: "doc",
              label: "Reference",
              id: "current/sdk/nodejs/reference/modules",
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
              id: "current/sdk/python/index",
            },
            "current/sdk/python/install",
            {
              type: "doc",
              label: "Get Started",
              id: "current/sdk/python/get-started",
            },
            {
              type: "link",
              label: "Reference 🔗",
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
              id: "current/sdk/elixir/index",
            },
            "current/sdk/elixir/install",
            {
              type: "doc",
              label: "Get Started",
              id: "current/sdk/elixir/get-started",
            },
            {
              type: "link",
              label: "Reference 🔗",
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
              id: "current/api/index",
            },
            "current/api/concepts",
            "current/api/playground",
            "current/api/build-custom-client",
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
          id: "current/cli/index",
        },
        "current/cli/install",
        "current/cli/run-pipelines-cli",
        {
          type: "doc",
          label: "Reference",
          id: "current/cli/reference",
        },
      ],
    },
    {
      type: "doc",
      id: "current/faq",
    },
    {
      type: "doc",
      id: "current/contributing",
    },
    {
      type: "link",
      label: "Changelog 🔗",
      href: "https://github.com/dagger/dagger/blob/main/CHANGELOG.md",
    },
  ],
  quickstart: [
    {
      type: "doc",
      id: "current/index",
      label: "Home",
    },
    {
      type: "category",
      label: "Quickstart",
      collapsible: false,
      collapsed: false,
      items: [
        "current/quickstart/index",
        "current/quickstart/basics",
        "current/quickstart/setup",
        "current/quickstart/cli",
        "current/quickstart/sdk",
        "current/quickstart/hello",
        "current/quickstart/test",
        "current/quickstart/build",
        "current/quickstart/publish",
        "current/quickstart/build-multi",
        "current/quickstart/caching",
        "current/quickstart/build-dockerfile",
        "current/quickstart/conclusion",
      ]
    }
  ],
  zenith: [
    {
      type: "doc",
      id: "zenith/index",
      label: "Introduction",
    },
    {
      type: "category",
      label: "Using Dagger",
      collapsible: true,
      collapsed: true,
      items: [
        {
          type: "doc",
          label: "Installation",
          id: "zenith/user/install",
        },
        {
          type: "category",
          label: "Quickstart",
          items: [
            "zenith/user/quickstart/index",
            "zenith/user/quickstart/setup",
            "zenith/user/quickstart/functions",
            "zenith/user/quickstart/call",
            "zenith/user/quickstart/download",
            "zenith/user/quickstart/shell",
            "zenith/user/quickstart/up",
            "zenith/user/quickstart/conclusion",
          ]
        },

      ],
    },
    {
      type: "category",
      label: "Programming Dagger",
      collapsible: true,
      collapsed: true,
      items: [
        {
          type: "doc",
          label: "Introduction",
          id: "zenith/developer/index",
        },
        {
          type: "category",
          label: "Go",
          items: [
            {
              type: "doc",
              id: "zenith/developer/go/quickstart",
            },
            {
              type: "doc",
              id: "zenith/developer/go/test-build-publish",
            },
            {
              type: "doc",
              id: "zenith/developer/go/advanced-programming",
            },
            {
              type: "link",
              label: "Go SDK Reference 🔗",
              href: "https://pkg.go.dev/dagger.io/dagger",
            },

          ]
        },
        {
          type: "category",
          label: "Python",
          items: [
            {
              type: "doc",
              id: "zenith/developer/python/quickstart",
            },
            {
              type: "doc",
              id: "zenith/developer/python/advanced-programming",
            },
            {
              type: "link",
              label: "Python SDK Reference 🔗",
              href: "https://dagger-io.readthedocs.org/",
            },
          ]
        },
        {
          type: "doc",
          id: "zenith/developer/publishing-modules",
        },
        {
          type: "doc",
          id: "zenith/developer/troubleshooting",
        },
      ],
    },
    {
      type: "category",
      label: "Reference",
      collapsible: true,
      collapsed: true,
      items: [
        {
          type: "doc",
          label: "CLI Reference",
          id: "current/cli/reference",
        },
        {
          type: "link",
          label: "API Reference 🔗",
          href: "https://docs.dagger.io/api/reference",
        },

      ],
    },
    {
      type: "doc",
      id: "current/faq",
    },
    {
      type: "doc",
      id: "current/contributing",
    },
    {
      type: "link",
      label: "Changelog 🔗",
      href: "https://github.com/dagger/dagger/blob/main/CHANGELOG.md",
    },
  ],
};
