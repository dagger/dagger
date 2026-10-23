import fs from "node:fs";
import path from "node:path";
import type { Plugin } from '@docusaurus/types';

type Options = {
  docsPath?: string;
};

type MdxFile = {
  fullPath: string;
  title: string;
  fallbackUrlPath: string;
  body: string;
};

// Thanks to @jharrell and Prisma team. Apache-2.0 content
// https://github.com/prisma/docs/commit/bc5b452b34ebdce8e89ccbe0c7f029e8b6ef1239
const llmsTxtPlugin = async function pluginLlmsTxt(
  context,
  options: Options = {}
): Promise<Plugin> {
  return {
    name: "llms-txt-plugin",
    loadContent: async () => {
      const { siteDir } = context;
      const contentDir = path.resolve(siteDir, options.docsPath ?? "current_docs");
      const allMdx: MdxFile[] = [];

      // recursive function to get all mdx files
      const getMdxFiles = async (dir: string) => {
        const entries = await fs.promises.readdir(dir, { withFileTypes: true });

        for (const entry of entries) {
          const fullPath = path.join(dir, entry.name);
          if (entry.isDirectory()) {
            await getMdxFiles(fullPath);
          } else if (entry.name.endsWith(".mdx")) {
            const content = await fs.promises.readFile(fullPath, "utf8");

            // extract title from frontmatter if it exists
            const titleMatch = content.match(
              /^---\n(?:.*\n)*?title:\s*["']?([^"'\n]+)["']?\n(?:.*\n)*?---\n/
            );
            const title = titleMatch ? titleMatch[1] : "";

            // Get the relative path for URL construction
            const relativePath = path.relative(contentDir, fullPath);

            // Convert file path to URL path by:
            // 1. Removing numeric prefixes (like 100-, 01-, etc.)
            // 2. Removing the .mdx extension
            // This is only a fallback for files that are not docs (partials);
            // docs use their permalink, resolved in postBuild.
            let urlPath = relativePath
              .replace(/^\d+-/, "")
              .replace(/\/\d+-/g, "/")
              .replace(/\.mdx$/, "");

            // strip frontmatter
            const contentWithoutFrontmatter = content.replace(/^---\n[\s\S]*?\n---\n/, "");

            allMdx.push({
              fullPath,
              title,
              fallbackUrlPath: `/${urlPath}`,
              body: contentWithoutFrontmatter,
            });
          }
        }
      };

      await getMdxFiles(contentDir);
      return { allMdx };
    },
    postBuild: async ({ content, routes, outDir, plugins }) => {
      const { allMdx } = content as { allMdx: MdxFile[] };
      const { siteDir, siteConfig } = context;

      // Docs are served at their `slug`, which need not match their file
      // path. current_docs is also served at /next, while the newest release
      // snapshot is served at the root. Link each doc to the root copy when
      // the release has it, and to the /next copy otherwise.
      type DocMetadata = { id: string; source: string; permalink: string };
      type LoadedVersion = { versionName: string; isLast: boolean; docs: DocMetadata[] };
      const docsContent = plugins.find(
        (plugin) => plugin.name === "docusaurus-plugin-content-docs"
      )?.content as { loadedVersions?: LoadedVersion[] } | undefined;
      const loadedVersions = docsContent?.loadedVersions ?? [];
      const rootPermalinks = new Map<string, string>(
        loadedVersions
          .find((version) => version.isLast)
          ?.docs.map((doc): [string, string] => [doc.id, doc.permalink])
      );
      const permalinkById = new Map<string, string>();
      const permalinkByFile = new Map<string, string>();
      for (const doc of loadedVersions.find((version) => version.versionName === "current")?.docs ?? []) {
        const permalink = rootPermalinks.get(doc.id) ?? doc.permalink;
        permalinkById.set(doc.id, permalink);
        permalinkByFile.set(path.resolve(siteDir, doc.source.replace(/^@site\//, "")), permalink);
      }

      // combine title and content with URL
      const fullMdx = allMdx.map(({ fullPath, title, fallbackUrlPath, body }) => {
        const fullUrl = `${siteConfig.url}${permalinkByFile.get(fullPath) ?? fallbackUrlPath}`;
        return title ? `# ${title}\n\nURL: ${fullUrl}\n${body}` : body;
      });

      // Write concatenated MDX content
      const concatenatedPath = path.join(outDir, "llms-full.txt");
      await fs.promises.writeFile(concatenatedPath, fullMdx.join("\n---\n\n"));

      // we need to dig down several layers:
      // find PluginRouteConfig marked by plugin.name === "docusaurus-plugin-content-docs"
      const docsPluginRouteConfig = routes.filter(
        (route) => route.plugin.name === "docusaurus-plugin-content-docs"
      )[0];

      // docsPluginRouteConfig has a routes property has a record with the path "/" that contains all docs routes.
      const allDocsRouteConfig =
        docsPluginRouteConfig.routes?.find((route) => {
          const version = route.props?.version as { name?: string; versionName?: string } | undefined;
          return version?.name === "current" || version?.versionName === "current";
        }) ??
        docsPluginRouteConfig.routes?.filter(
          (route) => route.path === "/"
        )[0];

      // A little type checking first
      if (!allDocsRouteConfig?.props?.version) {
        return;
      }

      // this route config has a `props` property that contains the current documentation.
      const versionProps = allDocsRouteConfig.props.version as Record<string, unknown>;
      const currentVersionDocsRoutes = versionProps.docs as Record<
        string,
        Record<string, unknown>
      >;

      // Docusaurus keys `docs` by doc id, so `Object.entries` yields an
      // alphabetical-by-id order. That buries "Getting Started" (the first
      // thing a coding agent reads) below entries like `config/*` and
      // `reference/*`. Instead, follow the sidebar order so the reading order
      // matches what humans and agents see in the navigation.
      type SidebarItem = {
        type?: string;
        href?: string;
        docId?: string;
        items?: SidebarItem[];
      };
      const docsSidebars = (versionProps.docsSidebars ?? {}) as Record<
        string,
        SidebarItem[]
      >;

      // Resolve a sidebar item to a doc-record key (doc id) when it points at a
      // doc. Leaf links expose `docId` directly; category index links only
      // expose a permalink via `href` (e.g. "/config"), so derive the doc id
      // from it (e.g. "config/index" or "config").
      const resolveDocId = (item: SidebarItem): string | undefined => {
        if (typeof item.docId === "string" && item.docId in currentVersionDocsRoutes) {
          return item.docId;
        }
        if (typeof item.href === "string") {
          const slug = item.href.replace(/^\/+/, "").replace(/\/+$/, "");
          for (const candidate of [`${slug}/index`, slug || "index"]) {
            if (candidate in currentVersionDocsRoutes) {
              return candidate;
            }
          }
        }
        return undefined;
      };

      const orderedPaths: string[] = [];
      const seen = new Set<string>();
      const collectSidebarLinks = (items: SidebarItem[] | undefined) => {
        if (!items) return;
        for (const item of items) {
          const docId = resolveDocId(item);
          if (docId && !seen.has(docId)) {
            seen.add(docId);
            orderedPaths.push(docId);
          }
          if (item.items) {
            collectSidebarLinks(item.items);
          }
        }
      };
      for (const sidebarItems of Object.values(docsSidebars)) {
        collectSidebarLinks(sidebarItems);
      }

      // Order the records by sidebar order, then append any docs that are not
      // referenced by a sidebar (sorted by id) so nothing is dropped.
      const orderedEntries: [string, Record<string, unknown>][] = [
        ...orderedPaths.map(
          (p) => [p, currentVersionDocsRoutes[p]] as [string, Record<string, unknown>]
        ),
        ...Object.entries(currentVersionDocsRoutes)
          .filter(([p]) => !seen.has(p))
          .sort(([a], [b]) => a.localeCompare(b)),
      ];

      // for every single docs route we now parse a path (which is the key) and a title
      const docsRecords = orderedEntries.map(([path, record]) => {
        const permalink = permalinkById.get(path);
        const url = permalink ? `${siteConfig.url}${permalink}` : path;
        return `- [${record.title}](${url}): ${record.description}`;
      });

      // Build up llms.txt file
      const llmsTxt = `# ${context.siteConfig.title}\n\n## Docs\n\n${docsRecords.join("\n")}`;

      // Write llms.txt file
      const llmsTxtPath = path.join(outDir, "llms.txt");
      await fs.promises.writeFile(llmsTxtPath, llmsTxt);
    },
  };
};

export default llmsTxtPlugin;
