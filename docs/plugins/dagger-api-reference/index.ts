import path from "path";
import type { LoadContext, Plugin } from "@docusaurus/types";

// eslint-disable-next-line @typescript-eslint/no-var-requires
const { parseSchema } = require("./schema.js");
// eslint-disable-next-line @typescript-eslint/no-var-requires
const defaultCoreTypes: string[] = require("./coreTypes.js");

export type DaggerApiOptions = {
  /** Path to the core schema SDL, relative to the site dir. */
  schemaPath?: string;
  /** Core type names to publish; defaults to ./coreTypes.js. */
  types?: string[];
};

/**
 * dagger-api-reference parses the core GraphQL schema once at build time and
 * exposes the normalized model as plugin global data. The reference React
 * components (src/components/api) read it via usePluginData, so the published
 * reference is generated straight from the schema — the same idea as Dang's
 * stdlib generator, done the Docusaurus-native way.
 */
export default function daggerApiReference(
  context: LoadContext,
  options: DaggerApiOptions = {}
): Plugin<unknown> {
  const schemaPath = path.resolve(
    context.siteDir,
    options.schemaPath ?? "docs-graphql/schema.graphqls"
  );
  const types = options.types ?? defaultCoreTypes;

  return {
    name: "dagger-api-reference",
    async loadContent() {
      return parseSchema(schemaPath, types);
    },
    async contentLoaded({ content, actions }) {
      actions.setGlobalData(content);
    },
    getPathsToWatch() {
      return [schemaPath];
    },
  };
}
