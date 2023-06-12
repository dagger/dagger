import { EntrypointMetadata } from "./entrypointMetadata"

/**
 * Entrypoint defines the patern of function supported by serveCommands.
 * This type shall change as the sdk supports more options and types of input.
 */ export function convertTsToGqlType(type: string): string {
  switch (type) {
    case "string":
      return "String"
    default:
      return "Unknown"
  }
}

export function entrypoinsMetadatatoGQLSchema(
  entrypoints: EntrypointMetadata[]
): string {
  let result = "extend type Query {\n"
  const indent = "  "

  const gqlEntrypoints = entrypoints.map((entrypoint): string => {
    let query = `${indent}"""\n${indent}${entrypoint.doc}\n${indent}"""\n`

    query += `${indent.repeat(2)}${entrypoint.name}(`
    query += entrypoint.args
      .slice(1)
      .map(
        (arg) =>
          `\n${indent.repeat(3)}"""\n${indent.repeat(3)}${
            arg.doc
          }\n${indent.repeat(3)}"""\n${indent.repeat(3)}${
            arg.name
          }: ${convertTsToGqlType(arg.type)}`
      )
    query += `\n${indent.repeat(2)}): ${convertTsToGqlType(entrypoint.return)}`

    return query
  })

  result += gqlEntrypoints.join(",\n")

  result += "\n}"
  return result
}
