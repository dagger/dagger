module.exports = {
  // Unified single sidebar
  current: [
    // ========================================
    // 📘 INTRODUCTION
    // ========================================
    {
      type: "category",
      label: "Introduction",
      collapsible: true,
      collapsed: false,
      items: ["index", "introduction/use-cases", "introduction/faq"],
    },

    // ========================================
    // 🚀 GETTING STARTED
    // ========================================
    {
      type: "category",
      label: "Getting Started",
      collapsible: true,
      collapsed: false,
      items: [
        "getting-started/index",
        "getting-started/installation",
        {
          type: "category",
          label: "Core Concepts",
          link: {
            type: "doc",
            id: "introduction/core-concepts/index",
          },
          collapsible: true,
          collapsed: false,
          items: [
            "introduction/core-concepts/toolchains",
            "introduction/core-concepts/checks",
            "introduction/core-concepts/functions",
          ],
        },
        {
          type: "category",
          label: "Quickstarts",
          collapsible: true,
          collapsed: true,
          items: [
            "getting-started/quickstarts/basics/index",
            "getting-started/quickstarts/blueprint/index",
            "getting-started/quickstarts/ci/index",
            "getting-started/quickstarts/agent/index",
            "getting-started/quickstarts/agent/inproject",
          ],
        },
        {
          type: "category",
          label: "Core Types",
          collapsible: true,
          collapsed: true,
          items: [
            "getting-started/types/index",
            "getting-started/types/container",
            "getting-started/types/directory",
            "getting-started/types/file",
            "getting-started/types/secret",
            "getting-started/types/service",
            "getting-started/types/llm",
            "getting-started/types/env",
            "getting-started/types/cache-volume",
            "getting-started/types/git-repository",
          ],
        },
        {
          type: "category",
          label: "Using Dagger",
          collapsible: true,
          collapsed: true,
          items: [
            "getting-started/api",
            "getting-started/api/clients-cli",
            "getting-started/api/clients-sdk",
            "getting-started/api/clients-http",
          ],
        },
      ],
    },

    // ========================================
    // ✨ FEATURES
    // ========================================
    {
      type: "category",
      label: "Features",
      link: {
        type: "doc",
        id: "introduction/features/index",
      },
      collapsible: true,
      collapsed: true,
      items: [
        {
          type: "category",
          label: "Core Features",
          collapsible: true,
          collapsed: false,
          items: [
            "introduction/features/programmability",
            "introduction/features/caching",
            "introduction/features/sandbox",
            "introduction/features/observability",
          ],
        },
        {
          type: "category",
          label: "Composition & Reusability",
          collapsible: true,
          collapsed: false,
          items: [
            "introduction/features/reusability",
            "introduction/features/services",
          ],
        },
        {
          type: "category",
          label: "Advanced Features",
          collapsible: true,
          collapsed: false,
          items: [
            "introduction/features/secrets",
            "introduction/features/shell",
            "introduction/features/llm",
          ],
        },
      ],
    },

    // ========================================
    // 🔧 BUILDING WITH DAGGER
    // ========================================
    {
      type: "category",
      label: "Building with Dagger",
      link: {
        type: "doc",
        id: "extending/index",
      },
      collapsible: true,
      collapsed: true,
      items: [
        {
          type: "category",
          label: "Module Development",
          collapsible: true,
          collapsed: true,
          items: [
            "extending/modules/modules",
            "extending/modules/functions",
            "extending/modules/arguments",
            "extending/modules/return-types",
            "extending/modules/chaining",
            "extending/modules/secrets",
            "extending/modules/services",
            "extending/modules/cache-volumes",
            "extending/modules/llm",
            "extending/modules/documentation",
            "extending/modules/remote-repositories",
            "extending/modules/module-dependencies",
            "extending/modules/packages",
            "extending/modules/constructors",
            "extending/modules/error-handling",
            "extending/modules/enumerations",
            "extending/modules/custom-types",
            "extending/modules/interfaces",
            "extending/modules/state",
            "extending/modules/function-caching",
            "extending/modules/playground",
            {
              type: "link",
              label: "Module Configuration Schema",
              href: "https://docs.dagger.io/reference/dagger.schema.json",
            },
          ],
        },
        {
          type: "category",
          label: "SDK Integration",
          collapsible: true,
          collapsed: true,
          items: [
            "extending/custom-applications/go",
            "extending/custom-applications/python",
            "extending/custom-applications/typescript",
            "extending/custom-applications/php",
          ],
        },
      ],
    },

    // ========================================
    // 🍳 COOKBOOK
    // ========================================
    {
      type: "doc",
      id: "cookbook/index",
      label: "Cookbook",
    },

    // ========================================
    // 📚 REFERENCE
    // ========================================
    {
      type: "category",
      label: "Reference",
      link: {
        type: "doc",
        id: "reference/index",
      },
      collapsible: true,
      collapsed: true,
      items: [
        "reference/glossary",
        "reference/troubleshooting",
        {
          type: "category",
          label: "CLI",
          collapsible: true,
          collapsed: true,
          items: ["reference/cli/index"],
        },
        {
          type: "category",
          label: "API & SDKs",
          collapsible: true,
          collapsed: true,
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
          label: "Configuration",
          collapsible: true,
          collapsed: true,
          items: [
            "reference/configuration/modules",
            "reference/configuration/cloud",
            "reference/configuration/cache",
            "reference/configuration/engine",
            "reference/configuration/llm",
            "reference/configuration/custom-runner",
            "reference/configuration/custom-ca",
            "reference/configuration/proxy",
            {
              type: "link",
              label: "Engine Configuration Schema",
              href: "https://docs.dagger.io/reference/engine.schema.json",
            },
          ],
        },
        {
          type: "category",
          label: "Deployment",
          collapsible: true,
          collapsed: true,
          items: [
            {
              type: "category",
              label: "Container Runtimes",
              collapsible: true,
              collapsed: true,
              items: [
                "reference/container-runtimes/index",
                "reference/container-runtimes/docker",
                "reference/container-runtimes/podman",
                "reference/container-runtimes/nerdctl",
                "reference/container-runtimes/apple-container",
              ],
            },
            "reference/deployment/kubernetes",
            "reference/deployment/openshift",
          ],
        },
        {
          type: "category",
          label: "CI/CD Integration",
          collapsible: true,
          collapsed: true,
          items: [
            "getting-started/ci-integrations/github-actions",
            "getting-started/ci-integrations/gitlab",
            "getting-started/ci-integrations/circleci",
            "getting-started/ci-integrations/jenkins",
            "getting-started/ci-integrations/azure-pipelines",
            "getting-started/ci-integrations/aws-codebuild",
            "getting-started/ci-integrations/argo-workflows",
            "getting-started/ci-integrations/tekton",
            "getting-started/ci-integrations/teamcity",
            "getting-started/ci-integrations/github",
          ],
        },
        {
          type: "category",
          label: "Best Practices",
          collapsible: true,
          collapsed: true,
          items: [
            "reference/best-practices/adopting",
            "reference/best-practices/modules",
            "reference/best-practices/monorepos",
            "reference/best-practices/contributing",
          ],
        },
        "reference/ide-setup",
      ],
    },
  ],
};
