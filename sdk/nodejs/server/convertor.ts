import { render, renderFn } from "micromustache"

import { EntrypointMetadata } from "./entrypointMetadata"

type Convertor = { [key: string]: () => string }

/**
 * Primitive type conversion
 */
const primitiveConvertor: Convertor = {
  string: () => "String",
  number: () => "Int",
  boolean: () => "Boolean",
}

/**
 * Entrypoint defines the pattern of function supported by serveCommands.
 * This type shall change as the sdk supports more options and types of input.
 *
 * If the type is wrapped in a Promise, the function will call itself recursively until
 * it unwrap all promises.
 */
export function convertTsToGqlType(type: string): string {
  if (type.startsWith("Promise")) {
    return convertTsToGqlType(type.slice(8, -1))
  }

  const primFct = primitiveConvertor[type]
  if (primFct) {
    return primFct()
  }

  return "Unknown"
}

type RenderFuncs = { [key: string]: (entrypoint: EntrypointMetadata) => string }

/**
 * Convert args into a GraphQL compliant type with their documentation.
 * @param e EntrypointMetadata
 * @returns GraphQL format arguments or an emptry string if there's no argument.
 */
function renderArgs(e: EntrypointMetadata) {
  if (e.args.length == 1) {
    return ""
  }

  return renderFn(
    `(
     {{args}}
    )`,
    () =>
      e.args
        .slice(1)
        .map((arg) =>
          render(
            `
      """
      {{doc}}
      """
      {{name}}: {{type}}`,
            { ...arg, type: convertTsToGqlType(arg.type) }
          )
        )
        .join(",\n")
  )
}

const renderFuncs: RenderFuncs = {
  doc: (e) => e.doc,
  name: (e) => e.name,
  return: (e) => convertTsToGqlType(e.return),
  arg: (e) => renderArgs(e),
}

/**
 * Convert entrypoint metadata into a valid GraphQL schema.
 *
 * @param entrypoints Functions to convert into GQL Schema
 * @returns string formatted as GraphQL schema
 */
export function entrypointsMetadatatoGQLSchema(
  entrypoints: EntrypointMetadata[]
): string {
  return renderFn(
    `
extend type Query {
  {{function}}
}`,
    () => {
      const fct = entrypoints.map((entrypoint) =>
        renderFn(
          `
    """
    {{doc}}
    """
    {{name}}{{arg}}: {{return}}
        `,
          (path) => renderFuncs[path](entrypoint),
          entrypoint
        )
      )

      return fct.join("\n")
    },
    entrypoints
  )
}
