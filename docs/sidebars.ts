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
        "adopting/triggers",
        "adopting/scaling",
        "adopting/engine-configuration",
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
        "using-dagger/shipping",
        "using-dagger/services",
      ],
    },

    // ========================================
    // DEVELOPING MODULES
    // ========================================
    {
      type: "category",
      label: "Developing Modules",
      link: {
        type: "doc",
        id: "extending/index",
      },
      collapsible: true,
      collapsed: true,
      items: [
        "extending/editions/dang",
        "extending/editions/go",
        "extending/editions/typescript",
        "extending/editions/python",
        "extending/testing",
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

    // ========================================
    // CORE CONCEPTS
    // ========================================
    {
      type: "category",
      label: "Core Concepts",
      link: {
        type: "doc",
        id: "introduction/core-concepts/index",
      },
      collapsible: true,
      collapsed: true,
      items: [
        "introduction/core-concepts/workspaces",
        "introduction/core-concepts/modules",
        "introduction/core-concepts/artifacts",
        "introduction/core-concepts/functions",
        "introduction/core-concepts/checks",
        "introduction/core-concepts/caching",
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
        // TODO: "reference/workspace-configuration" — .dagger/config.toml schema
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
