const path = require("path");
const fs = require("fs");
const utils = require("@docusaurus/utils");

module.exports = async function guidesPlugin(context, options) {
  const currentGuidesPath = path.resolve(options.currentGuidesPath);
  const zenithGuidesPath = path.resolve(options.zenithGuidesPath);
  const guidesJSONPath = "./static/guides.json";
  return {
    name: "docusaurus-plugin-guides",
    async loadContent() {
      const currentGuidesFolderPath = path.resolve(currentGuidesPath);
      const zenithGuidesFolderPath = path.resolve(zenithGuidesPath);

      const currentGuides = fs
        .readdirSync(currentGuidesFolderPath)
        .flatMap((x) => {
          const currentGuidePath = `${currentGuidesFolderPath}/${x}`;
          const isFile = fs.lstatSync(currentGuidePath).isFile();

          let content = "";

          if (isFile) {
            content = fs.readFileSync(currentGuidePath).toString();
          } else {
            return [];
          }

          const parsedMarkdown = utils.parseMarkdownString(content);
          const timestamp = Date.parse(parsedMarkdown.frontMatter.date);

          if (isNaN(timestamp)) {
            throw new Error(
              `Given date in ${x} is not ISO 8601 compatible. Please, set the date in this guide to MM-DD-YY format.`,
            );
          }

          return [
            {
              path: currentGuidePath,
              ...parsedMarkdown,
              timestamp,
            },
          ];
        });

      const zenithGuides = fs
        .readdirSync(zenithGuidesFolderPath)
        .flatMap((x) => {
          const zenithGuidePath = `${zenithGuidesFolderPath}/${x}`;
          const isFile = fs.lstatSync(zenithGuidePath).isFile();

          let content = "";

          if (isFile) {
            content = fs.readFileSync(zenithGuidePath).toString();
          } else {
            return [];
          }

          const parsedMarkdown = utils.parseMarkdownString(content);
          const timestamp = Date.parse(parsedMarkdown.frontMatter.date);

          if (isNaN(timestamp)) {
            throw new Error(
              `Given date in ${x} is not ISO 8601 compatible. Please, set the date in this guide to MM-DD-YY format.`,
            );
          }

          return [
            {
              path: zenithGuidePath,
              ...parsedMarkdown,
              timestamp,
            },
          ];
        });

      currentGuides.sort((a, b) => {
        return b.timestamp - a.timestamp;
      });

      zenithGuides.sort((a, b) => {
        return b.timestamp - a.timestamp;
      });

      let allTags = new Set();
      currentGuides.forEach((guide) =>
        guide.frontMatter.tags.forEach((tag) => allTags.add(tag)),
      );
      allTags = [...allTags];
      fs.writeFileSync(
        guidesJSONPath,
        JSON.stringify({currentGuides, zenithGuides, allTags}),
      );
    },
  };
};
