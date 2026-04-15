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
            "introduction/core-concepts/functions",
            "introduction/core-concepts/checks",
          ],
        },
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
