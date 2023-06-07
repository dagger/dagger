import fs from "fs"
import path from "path"
import ts from "typescript"

import Client from "./api/client.gen.js"
import { UnknownDaggerError } from "./common/errors/UnknownDaggerError.js"

/**
 * Entrypoint defines the patern of function supported by serveCommands.
 * This type shall change as the sdk supports more options and types of input.
 */
type Entrypoint = (client: Client, ...args: string[]) => string

type SerializedSymbol = {
  name: string
  type: ts.Type
  typeName: string
  doc: string
}

type SerializedSignature = {
  parameters: SerializedSymbol[]
  returnType: string
  doc: string
}

type Arg = {
  name: string
  type: string
  doc: string
}

type Return = string

/**
 * EntrypointMetadata defines the metadata of a function that acts
 * as an entrypoint.
 * It contains all important properties required to generate a GraphQL schema
 * from this.
 */
type EntrypointMetadata = {
  name: string
  doc: string
  args: Arg[]
  return: Return
}

function convertTsToGqlType(type: string): string {
  switch (type) {
    case "string":
      return "String"
    default:
      return "Unknown"
  }
}

function entrypoinsMetadatatoGQLSchema(
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

async function writeFile(content: string, dest: string): Promise<void> {
  fs.writeFileSync(dest, content, { mode: 0o600 })
}

async function listFiles(dir = "."): Promise<string[]> {
  const res = await Promise.all(
    fs.readdirSync(dir).map(async (file) => {
      const filepath = path.join(dir, file)

      // Ignore node_modules and transpiled typescript
      if (filepath.includes("node_modules") || filepath.includes("dist")) {
        return []
      }

      const stat = fs.statSync(filepath)

      if (stat.isDirectory()) {
        return await listFiles(filepath)
      }

      const allowedExtensions = [".ts", ".mts"]
      const ext = path.extname(filepath)
      if (allowedExtensions.find((allowedExt) => allowedExt === ext)) {
        return [file]
      }

      return []
    })
  )

  return res.reduce((p, c) => [...c, ...p], [])
}

function serializeSymbol(
  symbol: ts.Symbol,
  checker: ts.TypeChecker
): SerializedSymbol {
  if (!symbol.valueDeclaration) {
    throw new UnknownDaggerError("could not find symbol value declaration", {})
  }

  const type = checker.getTypeOfSymbolAtLocation(
    symbol,
    symbol.valueDeclaration
  )

  return {
    name: symbol.getName(),
    type: type,
    typeName: checker.typeToString(type),
    doc: ts.displayPartsToString(symbol.getDocumentationComment(checker)),
  }
}

function serializeSignature(
  signature: ts.Signature,
  checker: ts.TypeChecker
): SerializedSignature {
  return {
    parameters: signature.parameters.map((param) =>
      serializeSymbol(param, checker)
    ),
    returnType: checker.typeToString(signature.getReturnType()),
    doc: ts.displayPartsToString(signature.getDocumentationComment(checker)),
  }
}

export async function displaySchema(
  ...entrypoints: Entrypoint[]
): Promise<void> {
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
  // await writeFile(gqlSchema, "schema.graphql")
  await writeFile(gqlSchema, "/outputs/schema.graphql")
}

export async function serveCommands(
  ...entrypoints: Entrypoint[]
): Promise<void> {
  if (process.argv.length === 3 && process.argv[2] === "-schema") {
    await displaySchema(...entrypoints)
    return
  }

  throw new UnknownDaggerError("invalid command set to serveComamnds", {})
}
