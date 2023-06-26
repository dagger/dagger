import ts from "typescript"

import Client from "../api/client.gen.js"
import { UnknownDaggerError } from "../common/errors/UnknownDaggerError.js"
import { connect } from "../connect.js"
import { convertResult, entrypointsMetadatatoGQLSchema } from "./convertor.js"
import { Arg, EntrypointMetadata } from "./entrypointMetadata.js"
import { serializeSignature, serializeSymbol } from "./serialization.js"
import { listFiles, writeFile, readFile } from "./utils.js"

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type Entrypoint = (client: Client, ...args: string[]) => any

export type Resolver = {
  name: string
  namespace: string
}

export type Input = {
  // Use type any until we find a way to dynamically asserts type
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  args: { [key: string]: any }
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  parent: { [key: string]: any }

  resolver: string
}

export async function getMetadata(
  ...entrypoints: Entrypoint[]
): Promise<EntrypointMetadata[]> {
  const metadatas: EntrypointMetadata[] = []
  const files = await listFiles()

  if (files.length === 0) {
    throw new UnknownDaggerError("no files to introspect found", {})
  }

  // Interpret typescript source files to introspect functions.
  const program = ts.createProgram(files, {})
  const checker = program.getTypeChecker()

  for (const file of program.getSourceFiles()) {
    // Ignore type declaration files.
    if (file.isDeclarationFile) {
      continue
    }

    ts.forEachChild(file, introspect)
  }

  function introspect(node: ts.Node): void {
    // Handle functionn declaration
    if (ts.isFunctionDeclaration(node) && node.name) {
      const symbol = checker.getSymbolAtLocation(node.name)
      if (!symbol) {
        return
      }

      const symbolMetadata = serializeSymbol(symbol, checker)
      // Ignore functions that are not part of the entrypoint
      if (
        !entrypoints.find(
          (entrypoint) => entrypoint.name === symbolMetadata.name
        )
      ) {
        return
      }

      const signature = symbolMetadata.type
        .getCallSignatures()
        .map((metadata) => serializeSignature(metadata, checker))[0]

      const metadata: EntrypointMetadata = {
        name: symbolMetadata.name,
        doc: symbolMetadata.doc,
        args: signature.parameters.map((param) => ({
          doc: param.doc,
          type: param.typeName,
          name: param.name,
        })),
        return: signature.returnType,
      }

      metadatas.push(metadata)
    }
  }

  return metadatas
}

// parseResolver assumes that resolver has the format <Query.<function>>
// This function will evolves to supports namespace (class) in the future.
// The first member: `Query` will always be ignored.
function parseResolver(input: string): Resolver {
  // Split element by their separator
  const member = input.split(".")

  return {
    namespace: "",
    name: member[1],
  }
}

/**
 * Order arguments sent to the function in the right order.
 *
 * @param argsMetadata Metadata of the function arguments.
 * @param args input arguments.
 * @returns arguments values in a array sorted in the right order.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function orderAguments(argsMetadata: Arg[], args: { [key: string]: any }) {
  const keys = Object.keys(args)

  return argsMetadata.slice(1).map(({ name }) => {
    const key = keys.find((k) => k === name)
    if (!key) {
      return undefined
    }

    return args[key]
  })
}

/**
 * serveCommands allow Dagger to execute code-first function using dagger do.
 *
 * Forwarded functions MUST take as first parameter a Dagger client.
 *
 * @example
 * ```
 *  import Client, { serveCommands } from "@dagger.io/dagger"
 *
 *  serveCommands(hello)
 *
 *  function hello(_: Client, name: string): string {
 *    return `Hello ${ name }`
 *  }
 * ```
 *
 * @param entrypoints functions to transform into GraphQL query
 * @warning EXPERIMENTAL, this feature is not production ready
 */
export async function serveCommands(
  ...entrypoints: Entrypoint[]
): Promise<void> {
  if (process.argv.length === 3 && process.argv[2] === "-schema") {
    const metadata = await getMetadata(...entrypoints)
    const gqlSchema = entrypointsMetadatatoGQLSchema(metadata)
    await writeFile(gqlSchema, "/outputs/schema.graphql")

    return
  }

  const inputFile = await readFile("/inputs/dagger.json")
  const input: Input = JSON.parse(inputFile)
  const resolver = parseResolver(input.resolver)

  const fct = entrypoints.find(
    (entrypoint) => entrypoint.name === resolver.name
  )
  if (!fct) {
    throw new UnknownDaggerError("function to call not found", {})
  }

  const fctMetadata = (await getMetadata(fct))[0]
  const args = orderAguments(fctMetadata.args, input.args)

  await connect(async (client) => {
    const result = await fct.call(fct, client, ...args)
    const formattedResult = await convertResult(result)

    const output = JSON.stringify(formattedResult, null, 2)
    await writeFile(output, "/outputs/dagger.json")
  })
}
