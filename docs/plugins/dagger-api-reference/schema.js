// @ts-check
// Parse the core GraphQL schema (docs-graphql/schema.graphqls) into a normalized
// model the React reference components render. This is the Docusaurus-native
// analogue of Dang's stdlib generator (vito/dang#93): the schema is the single
// source of truth, so the reference can't drift from the API. Each field's
// signature comes from its argument/return types; its prose comes from the
// GraphQL `"""description"""`; Dagger's custom directives (@experimental,
// @deprecated, @expectedType, ...) become badges and type resolutions.

const fs = require("fs");
const {
  buildSchema,
  GraphQLObjectType,
  GraphQLNonNull,
  GraphQLList,
} = require("graphql");

// renderTypeRef turns a graphql-js type into a structured, link-aware token
// tree: { kind: 'named'|'list'|'nonNull', ... }. The component walks it to
// render `[Directory!]!` with each named type cross-linked.
function renderTypeRef(type, coreTypes, expectedType) {
  if (type instanceof GraphQLNonNull) {
    return { kind: "nonNull", of: renderTypeRef(type.ofType, coreTypes, expectedType) };
  }
  if (type instanceof GraphQLList) {
    return { kind: "list", of: renderTypeRef(type.ofType, coreTypes, expectedType) };
  }
  // A bare `ID` carrying @expectedType(name: "Directory") really means a
  // Directory — surface the real type, the way a reader thinks about it,
  // instead of the wire-level ID indirection.
  let name = type.name;
  if (name === "ID" && expectedType) {
    name = expectedType;
  }
  return { kind: "named", name, isCore: coreTypes.has(name) };
}

function directiveArgs(node) {
  const out = {};
  for (const arg of node.arguments || []) {
    // String/enum/int literals cover every directive arg in the core schema.
    out[arg.name.value] = arg.value.value ?? arg.value.name ?? null;
  }
  return out;
}

// directiveByName finds a directive application on a field/arg AST node.
function findDirective(astNode, name) {
  if (!astNode || !astNode.directives) return null;
  const d = astNode.directives.find((d) => d.name.value === name);
  return d ? directiveArgs(d) : null;
}

function expectedTypeOf(astNode) {
  const d = findDirective(astNode, "expectedType");
  return d ? d.name : null;
}

function buildArg(arg, coreTypes) {
  return {
    name: arg.name,
    description: arg.description || "",
    type: renderTypeRef(arg.type, coreTypes, expectedTypeOf(arg.astNode)),
    defaultValue:
      arg.astNode && arg.astNode.defaultValue
        ? printValueNode(arg.astNode.defaultValue)
        : undefined,
  };
}

// printValueNode renders a default-value AST node back to source-ish text
// (strings quoted, enums/ints/bools bare, lists/objects bracketed).
function printValueNode(node) {
  switch (node.kind) {
    case "StringValue":
      return JSON.stringify(node.value);
    case "IntValue":
    case "FloatValue":
    case "BooleanValue":
      return String(node.value);
    case "EnumValue":
      return node.value;
    case "NullValue":
      return "null";
    case "ListValue":
      return "[" + node.values.map(printValueNode).join(", ") + "]";
    case "ObjectValue":
      return (
        "{" +
        node.fields
          .map((f) => `${f.name.value}: ${printValueNode(f.value)}`)
          .join(", ") +
        "}"
      );
    default:
      return "";
  }
}

function buildField(field, coreTypes) {
  const experimental = findDirective(field.astNode, "experimental");
  return {
    name: field.name,
    description: field.description || "",
    args: field.args.map((a) => buildArg(a, coreTypes)),
    type: renderTypeRef(field.type, coreTypes, expectedTypeOf(field.astNode)),
    deprecated: field.deprecationReason
      ? { reason: field.deprecationReason }
      : null,
    experimental: experimental
      ? { reason: experimental.reason || "" }
      : null,
  };
}

/**
 * parseSchema reads the SDL at `schemaPath` and returns the model for the
 * given list of core type names. Cross-links are resolved against that same
 * list, so only types we actually publish become links.
 */
function parseSchema(schemaPath, coreTypeNames) {
  const sdl = fs.readFileSync(schemaPath, "utf8");
  const schema = buildSchema(sdl, { assumeValidSDL: true });
  const coreTypes = new Set(coreTypeNames);

  const types = {};
  for (const name of coreTypeNames) {
    const t = schema.getType(name);
    if (!(t instanceof GraphQLObjectType)) {
      throw new Error(`core type ${name} is not an object type in the schema`);
    }
    const fields = Object.values(t.getFields())
      .map((f) => buildField(f, coreTypes))
      .sort((a, b) => a.name.localeCompare(b.name));
    types[name] = {
      name,
      description: t.description || "",
      implements: t.getInterfaces().map((i) => i.name),
      fields,
    };
  }
  return { types, coreTypes: coreTypeNames };
}

module.exports = { parseSchema, renderTypeRef };
