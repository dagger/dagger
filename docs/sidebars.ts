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
      label: "Getting Started",
      items: [
        "installation",
        "quickstart/core-concepts/index",
        "quickstart/index",
      ],
    },
    {
      type: "category",
      label: "Use Cases",
      items: ["use-cases/agentic-ci", "use-cases/monorepos"],
    },
    {
      type: "category",
      label: "Components",
      items: [
        "components/index",
        "components/objects/create-your-own",
        {
          type: "category",
          label: "Objects",
          items: [
            "components/objects/container",
            "components/objects/directory",
            "components/objects/file",
            "components/objects/llm",
            "components/objects/secret",
            "components/objects/service",
            "components/objects/env",
          ],
        },
      ],
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
  examples: [
    {
      type: "category",
      label: "Examples",
      items: ["examples/index", "examples/demos", "examples/livestreams"],
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
        "reference/api/index",
        "reference/api/graphql",
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
