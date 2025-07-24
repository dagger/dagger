import path from "path";
import fs from "fs";
import { parseMarkdownFile } from "@docusaurus/utils/src/index";
import { PluginOptions, LoadContext } from "@docusaurus/types";

export type CookbookConfig = {
  cookbookPath: string;
};

type CookbookFrontMatter = {
  title: string;
  description: string;
  cookbook_tag: string;
  slug?: string;
};

export type CookbookFile = {
  path: string;
  frontMatter: CookbookFrontMatter;
  contentTitle: string;
  excerpt: string;
  firstHeading?: string;
};

const cookbookJSONPath = "./static/cookbook.json";

/**
 * This function generates a cookbook.json file in the ./static directory, based on the
 * cookbook files in the specified directory.
 * 
 * It scans all .mdx files in the cookbook directory, parses their frontmatter,
 * and groups them by cookbook_tag.
 */

export default async function cookbookPlugin(
  context: LoadContext,
  options: PluginOptions & CookbookConfig
) {
  const cookbookPath = options.cookbookPath;
  const {
    siteConfig: {
      markdown: { parseFrontMatter },
    },
  } = context;

  const tags = new Set<string>();
  const cookbookFiles: { [tag: string]: CookbookFile[] } = {};

  return {
    name: "docusaurus-plugin-cookbook",
    async loadContent() {
      const cookbookDirPath = path.resolve(cookbookPath);
      
      if (!fs.existsSync(cookbookDirPath)) {
        console.warn(`Cookbook directory not found: ${cookbookDirPath}`);
        return;
      }

      const files = fs.readdirSync(cookbookDirPath).filter(file => file.endsWith('.mdx'));
      
      await Promise.all(
        files.map(async (file) => {
          const filePath = path.join(cookbookDirPath, file);
          
          try {
            const fileContent = fs.readFileSync(filePath, 'utf8');
            
            const parsedMarkdown = await parseMarkdownFile({
              fileContent,
              filePath,
              parseFrontMatter,
            });

            const frontMatter = parsedMarkdown.frontMatter as CookbookFrontMatter;

            if (!frontMatter.cookbook_tag) {
              console.warn(`No cookbook_tag found in ${file}, skipping...`);
              return;
            }

            const tag = frontMatter.cookbook_tag;
            tags.add(tag);

            // Extract first ## heading from content
            const firstHeadingMatch = parsedMarkdown.content.match(/^##\s+(.+)$/m);
            const firstHeading = firstHeadingMatch ? firstHeadingMatch[1] : undefined;

            const cookbookFile: CookbookFile = {
              path: filePath,
              frontMatter,
              contentTitle: parsedMarkdown.contentTitle,
              excerpt: parsedMarkdown.excerpt,
              firstHeading,
            };

            if (!cookbookFiles[tag]) {
              cookbookFiles[tag] = [];
            }
            
            cookbookFiles[tag].push(cookbookFile);
          } catch (error) {
            console.error(`Error processing cookbook file ${file}:`, error);
          }
        })
      );

      // Sort files within each tag by title
      Object.keys(cookbookFiles).forEach(tag => {
        cookbookFiles[tag].sort((a, b) => 
          a.frontMatter.title.localeCompare(b.frontMatter.title)
        );
      });

      const json = {
        cookbookFiles,
        tags: Array.from(tags).sort(),
      };

      fs.writeFileSync(cookbookJSONPath, JSON.stringify(json, null, 2));
      console.log(`Generated cookbook.json with ${Object.keys(cookbookFiles).length} tags and ${Object.values(cookbookFiles).flat().length} files`);
    },
  };
}
