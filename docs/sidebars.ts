module.exports = {
  current: [
    {
      type: "category",
      label: "What is Dagger?",
      items: [
        "index",
        "features/programmability",
        "features/portability",
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
      items: ["examples/index", "examples/demos", "examples/livestreams"],
    },
  ],
  gettingStarted: [
    {
      type: "category",
      label: "Getting Started",
      items: [
        "getting-started/index",
        "getting-started/installation",
        "getting-started/core-concepts",
        "getting-started/quickstart",
      ],
    },
    {
      type: "category",
      label: "Use Cases",
      items: ["use-cases/agentic-ci", "use-cases/monorepos"],
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
    {
      type: "category",
      label: "Types",
      items: [
        "types/index",
        {
          type: "category",
          label: "Objects",
          items: [
            "types/objects/container",
            "types/objects/directory",
            "types/objects/file",
            "types/objects/llm",
            "types/objects/secret",
            "types/objects/service",
            "types/objects/env",
          ],
        },
      ],
    },
  ],
  extending: [
    {
      type: "category",
      label: "Extending Dagger",
      items: [
        "extending/index",
        "extending/arguments",
        "extending/default-paths",
        "extending/secrets",
        "extending/services",
        "extending/return-types",
        "extending/chaining",
        "extending/cache-volumes",
        "extending/documentation",
        "extending/llm",
        "extending/error-handling",
        "extending/enumerations",
        "extending/packages",
        "extending/custom-types",
        "extending/constructors",
        "extending/interfaces",
        {
          type: "link",
          label: "Dagger JSON Schema",
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
    {
      type: "category",
      label: "Clients",
      items: ["extending/clients-cli", "extending/clients-http"],
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
      label: "API Documentation",
      items: [
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
        "reference/api/module-registry",
      ],
    },
    {
      type: "category",
      label: "Engine & Runtime",
      items: [
        "reference/engine-runtime/index",
        "reference/engine-runtime/performance-caching",
        "reference/engine-runtime/troubleshooting",
        {
          type: "link",
          label: "Engine Configuration File Reference",
          href: "https://docs.dagger.io/reference/engine.schema.json",
        },
      ],
    },
  ],
  ci: [
    {
      type: "category",
      label: "CI",
      items: ["ci/adopting"],
    },
  ],
  cookbook: [
    {
      type: "category",
      label: "Cookbook",
      items: [
        "cookbook/index",
        "cookbook/filesystem",
        "cookbook/build",
        "cookbook/secrets",
        "cookbook/services",
        "cookbook/container-images",
      ],
    },
  ],
};
