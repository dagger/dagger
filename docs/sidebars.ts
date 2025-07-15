module.exports = {
  current: [
    {
      type: "category",
      label: "Getting Started",
      items: [
        "index",
        "installation",
        "quickstart/core-concepts/index",
        "quickstart/index",
        "ide-setup",
      ],
    },
    {
      type: "category",
      label: "Use Cases",
      items: ["use-cases/modern-ci", "use-cases/agentic-ci"],
    },
    {
      type: "category",
      label: "Components",
      items: [
        "components/functions",
        "components/objects/container",
        "components/objects/directory",
        "components/objects/file",
        "components/objects/secret",
        "components/objects/service",
        "components/objects/create-your-own",
      ],
    },
  ],
  examples: [
    {
      type: "category",
      label: "Examples",
      items: ["examples/index", "examples/demos", "examples/livestreams"],
    },
  ],
  extending: [
    {
      type: "category",
      label: "Extending Dagger",
      items: ["extending/index", "extending/arguments", "extending/return-types"],
    },
  ],
};
