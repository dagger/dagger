const path = require("path");
const fs = require("fs");
const utils = require("@docusaurus/utils");

module.exports = async function guidesPlugin(context, options) {
  const guidesJSONPath = "./static/guides.json";
  return {
    name: "docusaurus-plugin-guides",
    async loadContent() {
      const guidesFolderPath = path.resolve("../docs/current/guides");
      const guides = fs.readdirSync(guidesFolderPath).flatMap((x) => {
        const guidePath = `${guidesFolderPath}/${x}`;
        const isFile = fs.lstatSync(guidePath).isFile();
        let content = "";
        if (isFile) {
          content = fs.readFileSync(guidePath).toString();
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
            path: guidePath,
            ...parsedMarkdown,
            timestamp,
          },
        ];
      });
      guides.sort((a, b) => {
        return b.timestamp - a.timestamp;
      })
      fs.writeFileSync(guidesJSONPath, JSON.stringify({guides}));
    },
  };
};
