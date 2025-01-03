import { visit, SKIP } from "unist-util-visit";

const plugin = (options) => {
  const transformer = async (ast) => {
    visit(ast, 'code', (node) => {
      const templateMeta = (node.meta || '')
        // Allow escaping spaces
        .split(/(?<!\\) /g)
        .find((meta) => meta == "template");
      if (!templateMeta) {
        return SKIP;
      }

      node.value = node.value.replaceAll(/\{\{\s*(\S+)\s*\}\}/g, (_, key) => options[key]);
      return SKIP;
    });
  };
  return transformer;
};

export default plugin;
