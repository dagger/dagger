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
        "adopting/set-up-your-project",
        "adopting/secrets",
        "adopting/caching",
        "adopting/observability",
        "adopting/ci-integration",
        "adopting/engine-runtime",
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
            "introduction/core-concepts/workspaces",
            "introduction/core-concepts/modules",
            "introduction/core-concepts/functions",
            "introduction/core-concepts/checks",
          ],
        },
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
        "extending/guide",
        // TODO: split guide into focused pages per plan:
        // "extending/when-to-develop"
        // "extending/choosing-an-sdk"
        // "extending/designing-for-artifacts"
        // "extending/workspace-access"
        // "extending/collections"
        // "extending/verbs"
        // "extending/configuration"
        // "extending/testing"
        {
          type: "category",
          label: "SDK Guides",
          collapsible: true,
          collapsed: true,
          items: [
            "extending/custom-applications/go",
            "extending/custom-applications/python",
            "extending/custom-applications/typescript",
            "extending/custom-applications/php",
            // TODO: "extending/sdk-guides/dang"
          ],
        },
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
          items: ["reference/cli/index"],
        },
        "reference/configuration/modules",
        // TODO: "reference/workspace-configuration" — .dagger/config.toml schema
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
        "reference/upgrade-to-workspaces",
      ],
    },
  ],
};
