import path from "path";
import fs from "fs";
import { parseMarkdownFile } from "@docusaurus/utils/src/index";
import { PluginOptions, LoadContext } from "@docusaurus/types";

export type GuidesConfig = {
  versions: {
    guidesPath: string;
    versionName: string;
  }[];
};

type GuideFrontMatter = {
  slug: string;
  displayed_sidebar: string;
  category: string;
  authors: string[];
  date: string;
  tags?: string[];
};

export type Guide = {
  path: string;
  frontMatter: GuideFrontMatter;
  contentTitle: string;
  excerpt: string;
  timestamp: number;
};

const guidesJSONPath = "./static/guides.json";

/**
 *
 * This function generates a guides.json file in the ./static directory, based on the
 * guides declared in the docusaurus.config.ts file, in the plugins section.
 *
 * Using https://github.com/facebook/docusaurus/blob/main/packages/docusaurus-plugin-content-docs/src/index.ts
 * as a reference for writing and setting types to plugins.
 *
 */

export default async function guidesPlugin(
  context: LoadContext,
  options: PluginOptions & GuidesConfig
) {
  const versions = options.versions;
  const {
    siteConfig: {
      markdown: { parseFrontMatter },
    },
  } = context;

  const tags = new Set<string>();
  const guides = {};

  return {
    name: "docusaurus-plugin-guides",
    async loadContent() {
      await Promise.all(
        versions.flatMap(async (version) => {
          const versionGuidesPath = path.resolve(version.guidesPath);
          const versionGuides = [];
          await Promise.all(
            fs.readdirSync(versionGuidesPath).map(async (guide) => {
              const filePath = `${versionGuidesPath}/${guide}`;
              const isFile = fs.lstatSync(filePath).isFile();
              let fileContent: string;

              // omit directories
              if (isFile) {
                fileContent = fs.readFileSync(filePath).toString();
              } else {
                console.log("no file found");
                return;
              }

              const parsedMarkdown = await parseMarkdownFile({
                fileContent,
                filePath,
                parseFrontMatter,
              });

              const frontMatter =
                parsedMarkdown.frontMatter as GuideFrontMatter;

              const timestamp = Date.parse(frontMatter.date as string);

              if (isNaN(timestamp)) {
                throw new Error(
                  `Date in ${guide} is not ISO 8601 compatible. Please, set the date in this guide to MM-DD-YY format.`
                );
              }

              const guideTags = frontMatter.tags;
              if (guideTags) {
                guideTags.forEach((tag) => tags.add(tag));
              }

              versionGuides.push({
                path: filePath,
                ...parsedMarkdown,
                timestamp,
              });
            })
          );
          guides[version.versionName] = versionGuides;
        })
      );

      const json = { guides };

      if (tags.size > 0) {
        json["tags"] = tags;
      } else {
        json["tags"] = [];
      }

      fs.writeFileSync(guidesJSONPath, JSON.stringify(json));
    },
  };
}
