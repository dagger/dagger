module.exports = {
  current: [
    // ========================================
    // OVERVIEW
    // ========================================
    "index",

    // ========================================
    // INSTALLATION
    // ========================================
    "getting-started/installation",

    // ========================================
    // ADOPTING DAGGER
    // ========================================
    {
      type: "category",
      label: "Adopting Dagger",
      collapsible: true,
      collapsed: false,
      items: [
        "getting-started/quickstart",
        "adopting/workspace-setup",
        "adopting/secrets",
        "adopting/observability",
        {
          type: "category",
          label: "Triggers",
          link: {
            type: "doc",
            id: "adopting/triggers/index",
          },
          collapsible: true,
          collapsed: true,
          items: [
            "adopting/triggers/github-actions",
            "adopting/triggers/gitlab",
            "adopting/triggers/circleci",
            "adopting/triggers/jenkins",
            "adopting/triggers/azure-pipelines",
            "adopting/triggers/aws-codebuild",
            "adopting/triggers/argo-workflows",
            "adopting/triggers/tekton",
            "adopting/triggers/teamcity",
          ],
        },
        {
          type: "category",
          label: "Scaling",
          link: {
            type: "doc",
            id: "adopting/scaling/index",
          },
          collapsible: true,
          collapsed: true,
          items: [
            "adopting/scaling/kubernetes",
            "adopting/scaling/openshift",
          ],
        },
      ],
    },

    // ========================================
    // USING DAGGER
    // ========================================
    {
      type: "category",
      label: "Using Dagger",
      collapsible: true,
      collapsed: false,
      items: [
        "using-dagger/checking",
        "using-dagger/generating",
        "using-dagger/services",
      ],
    },

    // ========================================
    // MODULE DEVELOPER GUIDE
    // ========================================
    {
      type: "category",
      label: "Module Developer Guide",
      link: {
        type: "doc",
        id: "extending/index",
      },
      collapsible: true,
      collapsed: true,
      items: [
        {
          type: "category",
          label: "Platform Fundamentals",
          link: {
            type: "doc",
            id: "extending/platform-fundamentals/index",
          },
          collapsible: true,
          collapsed: false,
          items: [
            "extending/platform-fundamentals/workspaces",
            "extending/platform-fundamentals/modules",
            "extending/platform-fundamentals/functions",
            "extending/platform-fundamentals/checks",
            "extending/platform-fundamentals/caching",
            "extending/platform-model",
          ],
        },
        "extending/when-to-create-module",
        "extending/module-anatomy",
        "extending/design-principles",
        {
          type: "category",
          label: "Type System",
          link: {
            type: "doc",
            id: "extending/type-system/index",
          },
          collapsible: true,
          collapsed: true,
          items: [
            "extending/type-system/scalars-lists-nullability-defaults",
            "extending/type-system/core-types",
            "extending/type-system/user-defined-object-types",
            "extending/type-system/constructors-fields-methods",
            "extending/type-system/arguments-and-return-values",
            "extending/type-system/enums-and-validation",
            "extending/type-system/designing-for-composability",
            "extending/type-system/type-design-clinics",
          ],
        },
        "extending/objects-and-state",
        "extending/directives",
        "extending/interfaces",
        "extending/errors",
        "extending/platform-features",
        "extending/module-quality",
        {
          type: "category",
          label: "Types Reference",
          collapsible: true,
          collapsed: true,
          items: [
            "extending/types/index",
            "extending/types/container",
            "extending/types/directory",
            "extending/types/file",
            "extending/types/secret",
            "extending/types/service",
            "extending/types/cache-volume",
            "extending/types/git-repository",
            "extending/types/env",
            "extending/types/llm",
          ],
        },
      ],
    },
    {
      type: "category",
      label: "Standalone SDK Docs",
      link: {
        type: "doc",
        id: "extending/sdks/index",
      },
      collapsible: true,
      collapsed: true,
      items: [
        "extending/sdks/dang",
        "extending/sdks/go",
        "extending/sdks/typescript",
        "extending/sdks/python",
        "extending/sdks/java",
        "extending/sdks/php",
      ],
    },

    // ========================================
    // REFERENCE
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
        {
          type: "category",
          label: "CLI",
          collapsible: true,
          collapsed: true,
          items: [
            "reference/cli/index",
            "reference/cli/lockfiles",
          ],
        },
        "reference/configuration/modules",
        "reference/configuration/engine",
        "reference/configuration/cloud",
        "reference/configuration/cache",
        "reference/configuration/llm",
        "reference/configuration/custom-runner",
        "reference/configuration/custom-ca",
        "reference/configuration/proxy",
        "reference/upgrade-to-workspaces",
      ],
    },
  ],
};
