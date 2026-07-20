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
  const id = `extending/types/${typeSlug(name)}`;
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
        "using-dagger/changesets",
        "using-dagger/services",
        "using-dagger/calling-functions",
        "using-dagger/environments",
      ],
    },

    // ========================================
    // SETUP GUIDES
    // ========================================
    {
      type: "category",
      label: "Setup Guides",
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
        "modules/eslint",
        "modules/prettier",
        "modules/biomejs",
        "modules/shellcheck",
        "modules/psscriptanalyzer",
        "modules/helm",
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
          label: "How Dagger Works",
          link: {
            type: "doc",
            id: "extending/how-dagger-works/index",
          },
          collapsible: true,
          collapsed: true,
          items: [
            "extending/how-dagger-works/workspaces",
            "extending/how-dagger-works/modules",
            "extending/how-dagger-works/functions",
            "extending/how-dagger-works/checks",
            "extending/how-dagger-works/cache",
            "extending/how-dagger-works/execution",
          ],
        },
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
            "extending/type-system/core-types",
            "extending/type-system/scalars-lists-nullability-defaults",
            "extending/type-system/enums-and-validation",
            "extending/type-system/arguments-and-return-values",
            "extending/type-system/user-defined-object-types",
            "extending/type-system/constructors-fields-methods",
            "extending/type-system/designing-for-composability",
            "extending/type-system/type-design-clinics",
          ],
        },
        {
          type: "category",
          label: "SDKs",
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
          ],
        },
        {
          type: "category",
          label: "Types Reference",
          collapsible: true,
          collapsed: true,
          items: [
            "extending/types/index",
            ...promotedApiTypeItems,
            {
              type: "category",
              label: "Other types",
              collapsible: true,
              collapsed: true,
              items: otherApiTypeItems,
            },
            "extending/types/all",
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
          items: [
            "reference/cli/index",
            "reference/cli/lockfiles",
          ],
        },
        "reference/configuration/workspace",
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
