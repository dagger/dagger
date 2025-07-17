module.exports = {
  current: [
    {
      type: "category",
      label: "Getting Started",
      items: [
        "index",
        "installation",
        "quickstart/core-concepts/index",
        "quickstart/index",
        "ide-setup",
      ],
    },
    {
      type: "category",
      label: "Use Cases",
      items: ["use-cases/modern-ci", "use-cases/agentic-ci"],
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
            "components/objects/environment",
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
        "integrations/apple-container",
        "integrations/argo-workflows",
        "integrations/aws-codebuild",
        "integrations/circleci",
        "integrations/github-actions",
        "integrations/github",
        "integrations/gitlab",
        "integrations/google-cloud-run",
        "integrations/jenkins",
        "integrations/kubernetes",
        "integrations/nerdctl",
        "integrations/openshift",
        "integrations/podman",
        "integrations/tekton",
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
        "extending/return-types",
      ],
    },
  ],
  reference: [
    {
      type: "category",
      label: "Reference",
      items: ["reference/index", "reference/glossary"],
    },
    {
      type: "category",
      label: "CLI Reference",
      items: ["reference/cli/index"],
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
        "reference/engine-runtime/local-development",
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
  features: [
    {
      type: "category",
      label: "Features",
      items: ["features/index"],
    },
  ],
  configuration: [
    {
      type: "category",
      label: "Configuration",
      items: ["reference/configuration/index"],
    },
  ],
};
