module.exports = {
  current: [
    {
      type: "category",
      label: "Introduction",
      items: [
        "index",
        "use-cases",
      ]
    },
    {
      type: "category",
      label: "Features",
      items: [
        "features/programmability",
        "features/reusability",
        "features/caching",
        "features/observability",
        "features/security",
        "features/secrets",
        "features/llm",
        "features/shell",
      ],
    },
    {
      type: "category",
      label: "Examples",
      items: ["examples/index", "examples/demos"],
    },
  ],
  gettingStarted: [
    {
      type: "category",
      label: "Getting Started",
      items: [
        "getting-started/index",
        "getting-started/installation",
        "getting-started/api",
      ],
    },
    {
      type: "category",
      label: "Hands-on with Dagger",
      items: [
        "getting-started/basics",
        "getting-started/ci",
        "getting-started/agent/index",
        "getting-started/agent/inproject",
      ],
    },
    {
      type: "category",
      label: "Calling the Dagger API",
      items: [
        {
          type: "category",
          label: "Clients",
          items: [
            "extending/clients-sdk",
            "extending/clients-cli",
            "extending/clients-http",
          ],
        },
        {
          type: "category",
          label: "Types",
          items: [
            "types/index",
            "types/objects/container",
            "types/objects/directory",
            "types/objects/file",
            "types/objects/llm",
            "types/objects/secret",
            "types/objects/service",
            "types/objects/cache-volume",
            "types/objects/env",
          ],
        },
      ]
    },
    {
      type: "category",
      label: "Integrations",
      items: [
        "integrations/index",
        "integrations/ci",
        "integrations/container-runtimes",
      ],
    },
  ],
  extending: [
    {
      type: "category",
      label: "Extending Dagger",
      items: [
        "extending/index",
        "extending/modules",
        "extending/functions",
        "extending/arguments",
        "extending/return-types",
        "extending/chaining",
        "extending/secrets",
        "extending/services",
        "extending/cache-volumes",
        "extending/llm",
        "extending/documentation",
        "extending/remote-repositories",
        "extending/module-dependencies",
        "extending/packages",
        "extending/constructors",
        "extending/default-paths",
        "extending/fs-filters",
        "extending/error-handling",
        "extending/enumerations",
        "extending/custom-types",
        "extending/interfaces",
        "extending/state",
        {
          type: "link",
          label: "Module Configuration Schema",
          href: "https://docs.dagger.io/reference/dagger.schema.json",
        },
      ],
    },
    {
      type: "category",
      label: "Custom Applications",
      items: [
        "extending/custom-applications/index",
        "extending/custom-applications/go",
        "extending/custom-applications/python",
        "extending/custom-applications/typescript",
        "extending/custom-applications/php",
      ],
    },
  ],
  reference: [
    {
      type: "category",
      label: "Reference",
      items: [
        "reference/index",
        "reference/glossary",
        "reference/cli/index",
        "reference/ide-setup",
        "reference/troubleshooting",
      ],
    },
    {
      type: "category",
      label: "Configuration",
      items: [
        "reference/configuration/index",
        "reference/configuration/cloud",
        "reference/configuration/cache",
        "reference/configuration/engine",
        "reference/configuration/llm",
        "reference/configuration/modules",
        "reference/configuration/custom-runner",
        "reference/configuration/custom-ca",
        "reference/configuration/proxy",
      ],
    },
    {
      type: "category",
      label: "Container Runtimes",
      items: [
        "reference/container-runtimes/kubernetes",
        "reference/container-runtimes/podman",
        "reference/container-runtimes/nerdctl",
        "reference/container-runtimes/apple-container",
      ],
    },
    {
      type: "category",
      label: "Best Practices",
      items: [
        "reference/best-practices/adopting",
        "reference/best-practices/monorepos",
        "reference/best-practices/modules",
        "reference/best-practices/contributing",
      ],
    },
    {
      type: "category",
      label: "API and SDKs Documentation",
      items: [
        "reference/api/internals",
        {
          type: "link",
          label: "GraphQL API Reference",
          href: "https://docs.dagger.io/api/reference",
        },
        {
          type: "link",
          label: "Go SDK Reference",
          href: "https://pkg.go.dev/dagger.io/dagger",
        },
        {
          type: "link",
          label: "PHP SDK Reference",
          href: "https://docs.dagger.io/reference/php",
        },
        {
          type: "link",
          label: "Python SDK Reference",
          href: "https://dagger-io.readthedocs.org/",
        },
        {
          type: "doc",
          label: "TypeScript SDK Reference",
          id: "reference/typescript/modules",
        },
      ],
    },
    {
      type: "category",
      label: "Engine and Runtime",
      items: [
        "reference/engine-runtime/index",
        "reference/engine-runtime/performance-caching",
        {
          type: "link",
          label: "Engine Configuration Schema",
          href: "https://docs.dagger.io/reference/engine.schema.json",
        },
      ],
    },
  ],
  cookbook: [
    {
      type: "category",
      label: "Cookbook",
      items: [
        "cookbook/index",
        "cookbook/filesystems",
        "cookbook/containers",
        "cookbook/agents",
        "cookbook/secrets",
        "cookbook/services",
        "cookbook/builds",
        "cookbook/errors",
      ],
    },
  ],
};
