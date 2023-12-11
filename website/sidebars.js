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
              label: "Reference",
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
      label: "Changelog",
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
  labs: [
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
            "zenith/user/quickstart/cli",
            "zenith/user/quickstart/setup",
            "zenith/user/quickstart/hello",
            "zenith/user/quickstart/test",
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
              label: "Go SDK Reference",
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
              label: "Python SDK Reference",
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
          label: "API Reference",
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
      label: "Changelog",
      href: "https://github.com/dagger/dagger/blob/main/CHANGELOG.md",
    },
  ],
  0.2: [
    {
      type: "category",
      label: "Introduction",
      collapsible: false,
      collapsed: false,
      items: ["v0.2/overview"],
    },
    {
      type: "category",
      label: "Getting Started",
      collapsible: false,
      collapsed: false,
      items: [
        "v0.2/getting-started/install",
        "v0.2/getting-started/how-it-works",
        {
          type: "category",
          label: "Tutorial",
          items: [
            "v0.2/getting-started/tutorial/local-dev",
            "v0.2/getting-started/tutorial/ci-environment",
          ],
        },
        {
          type: "link",
          label: "Quickstart Templates",
          href: "/install#explore-our-templates",
        },
      ],
    },
    {
      type: "category",
      label: "Core Concepts",
      collapsible: false,
      collapsed: false,
      items: [
        "v0.2/core-concepts/vs",
        "v0.2/core-concepts/action",
        "v0.2/core-concepts/plan",
        "v0.2/core-concepts/client",
        "v0.2/core-concepts/secrets",
        "v0.2/core-concepts/what-is-cue",
        "v0.2/core-concepts/dagger-fs",
      ],
    },
    {
      type: "category",
      label: "Guides",
      collapsible: false,
      collapsed: false,
      items: [
        {
          type: "category",
          label: "Writing Actions",
          collapsible: true,
          collapsed: true,
          items: [{ type: "autogenerated", dirName: "v0.2/guides/actions" }],
        },
        {
          type: "category",
          label: "Caching/BuildKit",
          collapsible: true,
          collapsed: true,
          items: [{ type: "autogenerated", dirName: "v0.2/guides/buildkit" }],
        },
        {
          type: "category",
          label: "Logging/debugging",
          collapsible: true,
          collapsed: true,
          items: [{ type: "autogenerated", dirName: "v0.2/guides/logdebug" }],
        },
        {
          type: "category",
          label: "Concepts",
          collapsible: true,
          collapsed: true,
          items: [{ type: "autogenerated", dirName: "v0.2/guides/concepts" }],
        },
        {
          type: "category",
          label: "Docker engine",
          collapsible: true,
          collapsed: true,
          items: [{ type: "autogenerated", dirName: "v0.2/guides/docker" }],
        },
        {
          type: "category",
          label: "System",
          collapsible: true,
          collapsed: true,
          items: [{ type: "autogenerated", dirName: "v0.2/guides/system" }],
        },
      ],
    },
    {
      type: "category",
      label: "Guidelines",
      collapsible: false,
      collapsed: false,
      items: ["v0.2/guidelines/contributing", "v0.2/guidelines/coding-style"],
    },
    {
      type: "category",
      label: "References",
      collapsible: false,
      collapsed: false,
      items: [
        "v0.2/references/core-actions-reference",
        "v0.2/references/dagger-types-reference",
        "v0.2/references/13ec8-dagger-env-reference",
      ],
    },
    {
      type: "category",
      label: "Use Cases",
      collapsible: false,
      collapsed: false,
      link: {
        type: "generated-index",
        title: "Use Cases",
        description:
          "See how others are using Dagger for their CI/CD pipelines. This includes integrating with CI environments.",
      },
      items: [
        "v0.2/use-cases/go-docker-swarm",
        "v0.2/use-cases/go-docker-hub",
        "v0.2/use-cases/node-ci",
        "v0.2/use-cases/aws-sam",
      ],
    },
    {
      type: "link",
      label: "⬅️ Dagger 0.1",
      href: "/0.1",
    },
  ],
  0.1: [
    {
      type: "category",
      label: "Introduction",
      collapsible: true,
      items: ["v0.1/introduction/what_is", "v0.1/introduction/vs_old"],
    },
    {
      type: "doc",
      id: "v0.1/install",
    },
    {
      type: "category",
      label: "Learn Dagger",
      collapsible: true,
      collapsed: false,
      items: [
        "v0.1/learn/what_is_cue",
        "v0.1/learn/get-started",
        "v0.1/learn/google-cloud-run",
        "v0.1/learn/kubernetes",
        "v0.1/learn/aws-cloudformation",
        "v0.1/learn/github-actions",
        "v0.1/learn/dev-cue-package",
        "v0.1/learn/package-manager",
      ],
    },
    {
      type: "category",
      label: "Use Cases",
      collapsible: true,
      collapsed: true,
      items: ["v0.1/use-cases/ci"],
    },
    {
      type: "category",
      label: "Universe - API Reference",
      collapsible: true,
      collapsed: true,
      // generate the sidebar for reference doc automatically
      items: [
        {
          type: "autogenerated",
          dirName: "v0.1/reference",
        },
      ],
    },
    {
      type: "category",
      label: "Administrator Manual",
      collapsible: true,
      collapsed: true,
      items: ["v0.1/administrator/operator-manual"],
    },
    {
      type: "link",
      label: "Dagger 0.2 ➡️",
      href: "/0.2",
    },
  ],
};
