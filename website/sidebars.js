/**
 * Creating a sidebar enables you to:
 - create an ordered group of docs
 - render a sidebar for each doc of that group
 - provide next/previous navigation

 The sidebars can be generated from the filesystem, or explicitly defined here.

 Create as many sidebars as you want.
 */

module.exports = {
  europa: [
    {
      type: "doc",
      id: "migrate-from-dagger-0.1",
    },
    {
      type: "category",
      label: "Getting Started",
      collapsible: false,
      link: {
        type: 'doc',
        id: 'getting-started/index'
      },
      items: ["getting-started/local-dev", "getting-started/ci-environment", "getting-started/vs"],
    },
    {
      type: "category",
      label: "Core Concepts",
      collapsible: false,
      collapsed: false,
      link: {
        type: 'generated-index',
        title: 'Core Concepts',
      },
      items: [
        "core-concepts/plan",
        "core-concepts/client",
        "core-concepts/secrets",
        "core-concepts/container-images",
        "core-concepts/what-is-cue",
        "core-concepts/dagger-cue",
        "core-concepts/cli-telemetry",
      ],
    },
    {
      type: "category",
      label: "Use Cases",
      collapsible: false,
      collapsed: false,
      link: {
        type: 'generated-index',
        title: 'Use Cases',
        description:
          "See how others are using Dagger for their CI/CD pipelines. This includes integrating with CI environments.",
      },
      items: [
        "use-cases/go-docker-swarm",
      ],
    }
  ],
};
