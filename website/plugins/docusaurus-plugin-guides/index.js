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
        return [{
          path: guidePath,
          ...utils.parseMarkdownString(content),
        }];
      });      
       fs.writeFileSync(guidesJSONPath, JSON.stringify(guides))
    }
  };
};
