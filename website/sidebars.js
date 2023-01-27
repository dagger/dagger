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
      id: "current/quickstart/quickstart-introduction",
      label: "Quickstart",
    },
    {
      type: "category",
      label: "Go SDK",
      collapsible: false,
      collapsed: false,
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
          type: "doc",
          label: "Guides",
          id: "current/sdk/go/guides",
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
      collapsible: false,
      collapsed: false,
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
          label: "Guides",
          id: "current/sdk/nodejs/guides",
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
      collapsible: false,
      collapsed: false,
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
          type: "doc",
          label: "Guides",
          id: "current/sdk/python/guides",
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
      label: "GraphQL API",
      collapsible: false,
      collapsed: false,
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
    {
      type: "category",
      label: "CLI",
      collapsible: false,
      collapsed: false,
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
      type: "category",
      label: "CUE SDK",
      collapsible: false,
      collapsed: false,
      items: [
        {
          type: "doc",
          label: "Overview",
          id: "current/sdk/cue/index",
        },
        "current/sdk/cue/getting-started/install",
        {
          type: "doc",
          label: "Get Started",
          id: "current/sdk/cue/getting-started/get-started",
        },
        {
          type: "doc",
          label: "Guides",
          id: "current/sdk/cue/guides",
        },
        {
          type: "doc",
          label: "Reference",
          id: "current/sdk/cue/reference",
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
        "current/quickstart/quickstart-introduction",
        "current/quickstart/quickstart-basics",
        "current/quickstart/quickstart-setup",
        "current/quickstart/quickstart-sdk",
        "current/quickstart/quickstart-hello",
        "current/quickstart/quickstart-test",
        "current/quickstart/quickstart-build",
        "current/quickstart/quickstart-publish",
        "current/quickstart/quickstart-build-multi",
        "current/quickstart/quickstart-caching",
        "current/quickstart/quickstart-build-dockerfile",
        "current/quickstart/quickstart-conclusion",
      ]
    }
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
