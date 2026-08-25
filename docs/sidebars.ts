const path = require("path");
const { orderedTypeNames } = require(
  "./plugins/dagger-api-reference/schema.js"
);
const promotedApiTypes: string[] = require(
  "./plugins/dagger-api-reference/coreTypes.js"
);

const promotedApiTypeLabels: Record<string, string> = {
  Query: "Query (top-level)",
};

// Keep in sync with typeSlug in src/components/api/data.ts and
// plugins/dagger-api-reference/generate-stubs.js.
function typeSlug(name: string): string {
  return name
    .replace(/([a-z0-9])([A-Z])/g, "$1-$2")
    .replace(/([A-Z]+)([A-Z][a-z])/g, "$1-$2")
    .toLowerCase();
}

function apiTypeSidebarItem(name: string) {
  const id = `api/reference/${typeSlug(name)}`;
  const label = promotedApiTypeLabels[name];
  return label ? { type: "doc", id, label } : id;
}

const allApiTypes: string[] = orderedTypeNames(
  path.resolve(__dirname, "docs-graphql/schema.graphqls"),
  promotedApiTypes
);
const promotedApiTypeSet = new Set(promotedApiTypes);
const promotedApiTypeItems = promotedApiTypes.map(apiTypeSidebarItem);
const otherApiTypeItems = allApiTypes
  .filter((name) => !promotedApiTypeSet.has(name))
  .sort((a, b) => a.localeCompare(b))
  .map(apiTypeSidebarItem);

module.exports = {
  current: [
    // ========================================
    // GETTING STARTED
    // ========================================
    {
      type: "category",
      label: "Getting Started",
      collapsible: true,
      collapsed: false,
      items: [
        "getting-started/introduction",
        "getting-started/quickstart",
        "getting-started/workspace-setup",
        "getting-started/pre-push",
        "getting-started/post-push",
      ],
    },

    // ========================================
    // CLI
    // ========================================
    {
      type: "category",
      label: "CLI",
      collapsible: true,
      collapsed: false,
      items: [
        "cli/install",
        "cli/checking",
        "cli/generating",
        "cli/services",
        "cli/calling-functions",
        { type: "doc", id: "cli/reference/index", label: "Reference" },
      ],
    },

    // ========================================
    // CONFIGURATION
    // ========================================
    {
      type: "category",
      label: "Configuration",
      link: {
        type: "doc",
        id: "config/index",
      },
      collapsible: true,
      collapsed: true,
      items: [
        "config/environments",
        "config/user",
        "config/module-wiring",
        "config/migrate-dagger-json",
        {
          type: "category",
          label: "Reference",
          link: {
            type: "doc",
            id: "config/reference/index",
          },
          collapsible: true,
          collapsed: true,
          items: [
            "config/reference/dagger-toml",
          ],
        },
      ],
    },

    // ========================================
    // MODULES
    // ========================================
    {
      type: "category",
      label: "Modules",
      link: {
        type: "doc",
        id: "modules/index",
      },
      collapsible: true,
      collapsed: true,
      items: [
        "modules/go",
        "modules/deno",
        "modules/pytest",
        "modules/jest",
        "modules/vitest",
        "modules/playwright",
        "modules/eslint",
        "modules/prettier",
        "modules/biomejs",
        "modules/shellcheck",
        "modules/psscriptanalyzer",
        "modules/helm",
      ],
    },

    // ========================================
    // SDKS
    // ========================================
    {
      type: "category",
      label: "SDKs",
      link: {
        type: "doc",
        id: "sdks/index",
      },
      collapsible: true,
      collapsed: true,
      items: [
        "sdks/dang",
        "sdks/go",
        "sdks/typescript",
        "sdks/python",
        "sdks/java",
        "sdks/php",
        "sdks/elixir",
      ],
    },

    // ========================================
    // API
    // ========================================
    {
      type: "category",
      label: "API",
      link: {
        type: "doc",
        id: "api/index",
      },
      collapsible: true,
      collapsed: true,
      items: [
        {
          type: "category",
          label: "Reference",
          link: {
            type: "doc",
            id: "api/reference/index",
          },
          collapsible: true,
          collapsed: true,
          items: [
            ...promotedApiTypeItems,
            {
              type: "category",
              label: "Other types",
              collapsible: true,
              collapsed: true,
              items: otherApiTypeItems,
            },
            "api/reference/all",
          ],
        },
        { type: "doc", id: "api/clients/index", label: "Clients" },
      ],
    },
  ],
};
