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
    // CLI
    // ========================================
    {
      type: "category",
      label: "CLI",
      collapsible: true,
      collapsed: false,
      items: [
        "cli/checking",
        "cli/generating",
        "cli/changesets",
        "cli/services",
        "cli/module-wiring",
        "cli/calling-functions",
        "cli/environments",
        {
          type: "category",
          label: "Reference",
          link: {
            type: "doc",
            id: "cli/reference/index",
          },
          collapsible: true,
          collapsed: true,
          items: [
            "cli/reference/lockfiles",
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
