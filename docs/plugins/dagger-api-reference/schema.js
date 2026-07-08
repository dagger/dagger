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
  GraphQLInterfaceType,
  GraphQLInputObjectType,
  GraphQLEnumType,
  GraphQLScalarType,
  GraphQLNonNull,
  GraphQLList,
} = require("graphql");

const BUILTIN_SCALARS = new Set(["String", "Int", "Float", "Boolean", "ID"]);

// namedTypeOf unwraps NonNull/List wrappers to the underlying named type.
function namedTypeOf(type) {
  let t = type;
  while (t instanceof GraphQLNonNull || t instanceof GraphQLList) {
    t = t.ofType;
  }
  return t;
}

// classify maps a named type to the kind the UI cares about: a published core
// type (gets a link), or an enum / scalar / input / interface / plain object (each
// rendered with its own affordance — enums reveal their values, etc.).
function classify(schema, name, coreTypes) {
  if (coreTypes.has(name)) return "core";
  const t = schema.getType(name);
  if (t instanceof GraphQLEnumType) return "enum";
  if (t instanceof GraphQLScalarType) return "scalar";
  if (t instanceof GraphQLInputObjectType) return "input";
  if (t instanceof GraphQLInterfaceType) return "interface";
  return "object";
}

// renderTypeRef turns a graphql-js type into a structured, link-aware token
// tree: { kind: 'named'|'list'|'nonNull', ... }. The component walks it to
// render `[Directory!]!` with each named type cross-linked. Named tokens also
// carry the resolved type's `named` kind so the UI can reveal enum values,
// scalar descriptions, and input object schemas inline.
function renderTypeRef(schema, type, coreTypes, expectedType, seen) {
  if (type instanceof GraphQLNonNull) {
    return {
      kind: "nonNull",
      of: renderTypeRef(schema, type.ofType, coreTypes, expectedType, seen),
    };
  }
  if (type instanceof GraphQLList) {
    return {
      kind: "list",
      of: renderTypeRef(schema, type.ofType, coreTypes, expectedType, seen),
    };
  }
  // A bare `ID` carrying @expectedType(name: "Directory") really means a
  // Directory — surface the real type, the way a reader thinks about it,
  // instead of the wire-level ID indirection.
  let name = type.name;
  if (name === "ID" && expectedType) {
    name = expectedType;
  }
  const named = classify(schema, name, coreTypes);
  if (seen) {
    if (named === "enum") seen.enums.add(name);
    if (named === "input") seen.inputs.add(name);
    if (named === "scalar" && !BUILTIN_SCALARS.has(name)) {
      seen.scalars.add(name);
    }
  }
  return { kind: "named", name, named, isCore: named === "core" };
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

function buildArg(schema, arg, coreTypes, seen) {
  return {
    name: arg.name,
    description: arg.description || "",
    type: renderTypeRef(
      schema,
      arg.type,
      coreTypes,
      expectedTypeOf(arg.astNode),
      seen
    ),
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

function buildField(schema, field, coreTypes, seen) {
  const experimental = findDirective(field.astNode, "experimental");
  const defaultPath = findDirective(field.astNode, "defaultPath");
  const defaultAddress = findDirective(field.astNode, "defaultAddress");
  const cache = findDirective(field.astNode, "cache");

  // Dagger-specific behaviors worth a one-line note: contextual defaults and
  // explicit cache control. These read straight off the directive arguments.
  const notes = [];
  if (defaultPath) {
    notes.push(`Defaults to a contextual path: \`${defaultPath.path}\``);
  }
  if (defaultAddress) {
    notes.push(`Defaults to a container address: \`${defaultAddress.address}\``);
  }
  if (cache) {
    const ttl = cache.ttl ? `, ttl \`${cache.ttl}\`` : "";
    notes.push(`Cached with the \`${cache.policy || "Default"}\` policy${ttl}.`);
  }

  return {
    name: field.name,
    description: field.description || "",
    args: field.args.map((a) => buildArg(schema, a, coreTypes, seen)),
    type: renderTypeRef(
      schema,
      field.type,
      coreTypes,
      expectedTypeOf(field.astNode),
      seen
    ),
    deprecated: field.deprecationReason
      ? { reason: field.deprecationReason }
      : null,
    experimental: experimental ? { reason: experimental.reason || "" } : null,
    notes,
  };
}

function buildInput(schema, input, coreTypes, seen) {
  return {
    name: input.name,
    description: input.description || "",
    fields: Object.values(input.getFields()).map((field) => ({
      name: field.name,
      description: field.description || "",
      type: renderTypeRef(
        schema,
        field.type,
        coreTypes,
        expectedTypeOf(field.astNode),
        seen
      ),
      defaultValue:
        field.astNode && field.astNode.defaultValue
          ? printValueNode(field.astNode.defaultValue)
          : undefined,
    })),
  };
}

/**
 * parseSchema reads the SDL at `schemaPath` and returns the model for the
 * given list of core type names. Cross-links are resolved against that same
 * list, so only types we actually publish become links.
 */
function isPublishedType(t) {
  if (t instanceof GraphQLInterfaceType && !t.name.startsWith("__")) {
    return true;
  }
  return (
    t instanceof GraphQLObjectType &&
    !t.name.startsWith("__") &&
    t.getInterfaces().some((i) => i.name === "Node")
  );
}

// reverseRefs scans every object/interface type in the schema and records, for
// each published API type, the fields that return it and the fields that accept
// it as an argument.
// This is what powers the "Returned by" / "Accepted by" sections — the kind of
// cross-reference a reader can't easily reconstruct themselves. @expectedType
// is honored so an `ID` argument counts toward its real type.
function reverseRefs(schema, coreTypes) {
  const returnedBy = {};
  const argOf = {};
  for (const name of coreTypes) {
    returnedBy[name] = [];
    argOf[name] = [];
  }

  for (const t of Object.values(schema.getTypeMap())) {
    if (!(t instanceof GraphQLObjectType || t instanceof GraphQLInterfaceType)) {
      continue;
    }
    if (t.name.startsWith("__")) continue;
    for (const field of Object.values(t.getFields())) {
      const ret = namedTypeOf(field.type).name;
      // Builder-style fields like Foo.withBar: Foo! are already visible on Foo.
      if (returnedBy[ret] && ret !== t.name) {
        returnedBy[ret].push({ type: t.name, field: field.name });
      }
      for (const arg of field.args) {
        const expected = expectedTypeOf(arg.astNode);
        let argName = namedTypeOf(arg.type).name;
        if (argName === "ID" && expected) argName = expected;
        if (argOf[argName]) {
          argOf[argName].push({
            type: t.name,
            field: field.name,
            arg: arg.name,
          });
        }
      }
    }
  }

  const byTypeField = (a, b) =>
    a.type.localeCompare(b.type) || a.field.localeCompare(b.field);
  for (const name of coreTypes) {
    returnedBy[name].sort(byTypeField);
    argOf[name].sort(byTypeField);
  }
  return { returnedBy, argOf };
}

// resolveTypeList returns the full, ordered set of API types to publish: the
// `featured` names first (in the given order, used for prominence), then every
// other Node object type and interface in the schema, alphabetically. Because
// the tail comes straight from the schema, a newly added type can never be
// silently omitted; it just lands at the end of the list.
function resolveTypeList(schema, featured) {
  const all = Object.values(schema.getTypeMap())
    .filter(isPublishedType)
    .map((t) => t.name);
  const allSet = new Set(all);

  const featuredPresent = featured.filter((n) => allSet.has(n));
  const featuredSet = new Set(featuredPresent);
  const rest = all
    .filter((n) => !featuredSet.has(n))
    .sort((a, b) => a.localeCompare(b));
  return [...featuredPresent, ...rest];
}

// orderedTypeNames is the file-based entry point used by the stub generator:
// build the schema and return the resolved, ordered type list.
function orderedTypeNames(schemaPath, featured) {
  const schema = buildSchema(fs.readFileSync(schemaPath, "utf8"), {
    assumeValidSDL: true,
  });
  return resolveTypeList(schema, featured);
}

/**
 * parseSchema reads the SDL at `schemaPath` and returns the model for the
 * resolved API type list (the `featured` names first, then every other
 * publishable type in the schema). Cross-links are resolved against that full
 * list.
 */
function parseSchema(schemaPath, featured) {
  const sdl = fs.readFileSync(schemaPath, "utf8");
  const schema = buildSchema(sdl, { assumeValidSDL: true });
  const typeNames = resolveTypeList(schema, featured);
  const coreTypes = new Set(typeNames);
  const seen = {
    enums: new Set(),
    scalars: new Set(),
    inputs: new Set(),
  };

  const { returnedBy, argOf } = reverseRefs(schema, coreTypes);

  const types = {};
  for (const name of typeNames) {
    const t = schema.getType(name);
    if (!(t instanceof GraphQLObjectType || t instanceof GraphQLInterfaceType)) {
      throw new Error(`API type ${name} is not an object or interface type`);
    }
    const fields = Object.values(t.getFields())
      .map((f) => buildField(schema, f, coreTypes, seen))
      .sort((a, b) => a.name.localeCompare(b.name));
    types[name] = {
      name,
      description: t.description || "",
      implements: t.getInterfaces().map((i) => i.name),
      fields,
      returnedBy: returnedBy[name],
      argOf: argOf[name],
    };
  }

  // Input objects referenced by published fields are documented inline where
  // their type names appear. Building an input can discover nested enums or
  // input objects, so walk the queue until it stops growing.
  const inputs = {};
  const inputQueue = Array.from(seen.inputs);
  for (let i = 0; i < inputQueue.length; i++) {
    const name = inputQueue[i];
    if (inputs[name]) continue;
    const input = schema.getType(name);
    if (!(input instanceof GraphQLInputObjectType)) continue;
    inputs[name] = buildInput(schema, input, coreTypes, seen);
    for (const discovered of seen.inputs) {
      if (!inputs[discovered] && !inputQueue.includes(discovered)) {
        inputQueue.push(discovered);
      }
    }
  }

  // Only the enums actually referenced by published fields are included, each
  // with its values and their descriptions, so the UI can reveal them inline.
  const enums = {};
  for (const name of seen.enums) {
    const e = schema.getType(name);
    if (!(e instanceof GraphQLEnumType)) continue;
    const values = e.getValues().map((v) => {
      const enumValue = findDirective(v.astNode, "enumValue");
      return {
        name: v.name,
        description: v.description || "",
        deprecated: v.deprecationReason || null,
        enumValue: enumValue ? enumValue.value : null,
      };
    });
    const visibleValues = values.some((v) => v.enumValue)
      ? values.filter((v) => v.enumValue)
      : values;
    enums[name] = {
      name,
      description: e.description || "",
      values: visibleValues,
    };
  }

  // Only custom scalars referenced by published fields are included. Built-in
  // GraphQL scalars (String/Int/etc.) are familiar enough to stay bare.
  const scalars = {};
  for (const name of seen.scalars) {
    const scalar = schema.getType(name);
    if (!(scalar instanceof GraphQLScalarType)) continue;
    scalars[name] = {
      name,
      description: scalar.description || "",
    };
  }

  return { types, enums, scalars, inputs, coreTypes: typeNames };
}

module.exports = { parseSchema, renderTypeRef, resolveTypeList, orderedTypeNames };
