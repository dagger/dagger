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
  const id = `reference/api/${typeSlug(name)}`;
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
        {
          type: "doc",
          id: "getting-started/install",
          label: "Install",
        },
        "getting-started/try-dagger",
        "getting-started/quickstart",
        "getting-started/cloud-checks",
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
        "using/checking",
        "using/generating",
        "using/services",
        "using/calling-functions",
      ],
    },

    // ========================================
    // CONFIGURING YOUR WORKSPACE
    // ========================================
    {
      type: "category",
      label: "Configuring your Workspace",
      link: {
        type: "doc",
        id: "config/index",
      },
      collapsible: true,
      collapsed: true,
      items: [
        "config/environments",
        "config/includes",
        "config/user",
        "config/module-wiring",
        "config/migrate-dagger-json",
      ],
    },

    // ========================================
    // REFERENCE
    // ========================================
    {
      type: "category",
      label: "Reference",
      collapsible: true,
      collapsed: true,
      items: [
        { type: "doc", id: "reference/cli/index", label: "CLI" },
        {
          type: "category",
          label: "Modules",
          link: {
            type: "doc",
            id: "reference/modules/index",
          },
          collapsible: true,
          collapsed: true,
          items: [
            "reference/modules/go",
            "reference/modules/deno",
            "reference/modules/pytest",
            "reference/modules/jest",
            "reference/modules/vitest",
            "reference/modules/playwright",
            "reference/modules/eslint",
            "reference/modules/prettier",
            "reference/modules/biomejs",
            "reference/modules/shellcheck",
            "reference/modules/psscriptanalyzer",
            "reference/modules/helm",
          ],
        },
        {
          type: "category",
          label: "SDKs",
          link: {
            type: "doc",
            id: "reference/sdks/index",
          },
          collapsible: true,
          collapsed: true,
          items: [
            "reference/sdks/dang",
            "reference/sdks/go",
            "reference/sdks/typescript",
            "reference/sdks/python",
            "reference/sdks/java",
            "reference/sdks/php",
            "reference/sdks/elixir",
          ],
        },
        {
          type: "category",
          label: "API",
          link: {
            type: "doc",
            id: "reference/api/index",
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
            "reference/api/all",
          ],
        },
        {
          type: "doc",
          id: "reference/client-libraries/index",
          label: "Client libraries",
        },
        {
          type: "category",
          label: "Configuration files",
          link: {
            type: "doc",
            id: "reference/config-files/index",
          },
          collapsible: true,
          collapsed: true,
          items: [
            "reference/config-files/dagger-toml",
          ],
        },
      ],
    },

    // ========================================
    // SELF-HOSTING
    // ========================================
    // Docusaurus renders a category with no items as a plain link, so this is a
    // doc entry until the section has pages. custom.scss styles a level-1 link
    // as a section label, the same as a level-1 category.
    { type: "doc", id: "self-hosting/index", label: "Self-hosting" },
  ],
};
