import ts from "typescript"

import Client from "../api/client.gen.js"
import { UnknownDaggerError } from "../common/errors/UnknownDaggerError.js"
import { connect } from "../connect.js"
import { entrypoinsMetadatatoGQLSchema } from "./convertor.js"
import { EntrypointMetadata } from "./entrypointMetadata.js"
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

export async function getSchema(...entrypoints: Entrypoint[]): Promise<void> {
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

  const gqlSchema = entrypoinsMetadatatoGQLSchema(metadatas)
  await writeFile(gqlSchema, "/outputs/schema.graphql")
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

export async function serveCommands(
  ...entrypoints: Entrypoint[]
): Promise<void> {
  console.log("args:", process.argv)
  if (process.argv.length === 3 && process.argv[2] === "-schema") {
    await getSchema(...entrypoints)
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

  await connect(async (client) => {
    const result = await fct.call(fct, client, ...Object.values(input.args))

    console.log(result)
    const output = JSON.stringify(result, null, 2)
    await writeFile(output, "/outputs/dagger.json")
  })
}
