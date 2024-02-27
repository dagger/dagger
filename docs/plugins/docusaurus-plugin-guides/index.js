const path = require("path");
const fs = require("fs");
const utils = require("@docusaurus/utils");

module.exports = async function guidesPlugin(context, options) {
  const currentGuidesPath = path.resolve(options.currentGuidesPath);
  //const versionedGuidesPath = path.resolve(options.versionedGuidesPath);
  const guidesJSONPath = "./static/guides.json";
  return {
    name: "docusaurus-plugin-guides",
    async loadContent() {
      const currentGuidesFolderPath = path.resolve(currentGuidesPath);
      //const versionedGuidesFolderPath = path.resolve(versionedGuidesPath);

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
              `Given date in ${x} is not ISO 8601 compatible. Please, set the date in this guide to MM-DD-YY format.`
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

      /*
      const versionedGuides = fs
        .readdirSync(versionedGuidesFolderPath)
        .flatMap((x) => {
          const versionedGuidePath = `${versionedGuidesFolderPath}/${x}`;
          const isFile = fs.lstatSync(versionedGuidePath).isFile();
          const isMarkdown =
            versionedGuidePath.endsWith(".md") ||
            versionedGuidePath.endsWith(".mdx");

          let content = "";

          if (isFile && isMarkdown) {
            content = fs.readFileSync(versionedGuidePath).toString();
          } else {
            return [];
          }

          const parsedMarkdown = utils.parseMarkdownString(content);
          const timestamp = Date.parse(parsedMarkdown.frontMatter.date);

          if (isNaN(timestamp)) {
            throw new Error(
              `Given date in ${x} is not ISO 8601 compatible. Please, set the date in this guide to MM-DD-YY format.`
            );
          }

          return [
            {
              path: versionedGuidePath,
              ...parsedMarkdown,
              timestamp,
            },
          ];
        });
        */

      currentGuides.sort((a, b) => {
        return b.timestamp - a.timestamp;
      });

      /*
      versionedGuides.sort((a, b) => {
        return b.timestamp - a.timestamp;
      });
      */

      let allTags = new Set();
      currentGuides.forEach((guide) =>
        guide.frontMatter.tags.forEach((tag) => allTags.add(tag))
      );
      allTags = [...allTags];
      fs.writeFileSync(
        guidesJSONPath,
        //JSON.stringify({ currentGuides, versionedGuides, allTags })
        JSON.stringify({ currentGuides, versionedGuides, allTags })
      );
    },
  };
};
