import ts from "typescript"

import { UnknownDaggerError } from "../../common/errors/UnknownDaggerError.js"
import { ClassTypeDef, FunctionTypedef } from "./typeDefs.js"
import { isMainObject, isObject } from "./utils.js"
import { Object } from "./abtractions/object.js"

export type ScanResult = {
  module: {
    description?: string
  }
  classes: { [name: string]: ClassTypeDef }
  functions: { [name: string]: FunctionTypedef }
}

/**
 * Scan the list of TypeScript File using the TypeScript compiler API.
 *
 * This function introspect files and returns metadata of their class and
 * functions that should be exposed to the Dagger API.
 *
 * WARNING(28/11/23): This does NOT include arrow style function.
 *
 * @param files List of TypeScript files to introspect.
 * @param moduleName The name of the module to introspect.
 */
export function scan(files: string[], moduleName = ""): ScanResult {
  if (files.length === 0) {
    throw new UnknownDaggerError("no files to introspect found", {})
  }

  // Interpret the given typescript source files.
  const program = ts.createProgram(files, { experimentalDecorators: true })
  const checker = program.getTypeChecker()

  const metadata: ScanResult = {
    module: {},
    classes: {},
    functions: {},
  }

  for (const file of program.getSourceFiles()) {
    // Ignore type declaration files.
    if (file.isDeclarationFile) {
      continue
    }

    ts.forEachChild(file, (node) => {
      // Handle class
      if (ts.isClassDeclaration(node) && isObject(node)) {
        const classTypeDef = introspectClass(checker, node)

        if (isMainObject(classTypeDef.name, moduleName)) {
          metadata.module.description = introspectTopLevelComment(file)
        }

        metadata.classes[classTypeDef.name] = classTypeDef
      }
    })
  }

  return metadata
}

/**
 * Introspect a class and return its metadata.
 *
 * This function goes throw all class' method that have the @fct decorator
 * and all its public properties.
 *
 * This function throws an error if it cannot read its symbol.
 *
 * @param checker The typescript compiler checker.
 * @param node The class to check.
 */
function introspectClass(
  checker: ts.TypeChecker,
  node: ts.ClassDeclaration,
): ClassTypeDef {
  return new Object(checker, node).typeDef
}

/**
 * Return the content of the top level comment of the given file.
 *
 * @param file The file to introspect.
 */
function introspectTopLevelComment(file: ts.SourceFile): string | undefined {
  const firstStatement = file.statements[0]
  if (!firstStatement) {
    return undefined
  }

  const commentRanges = ts.getLeadingCommentRanges(
    file.getFullText(),
    firstStatement.pos,
  )
  if (!commentRanges || commentRanges.length === 0) {
    return undefined
  }

  const commentRange = commentRanges[0]
  const comment = file
    .getFullText()
    .substring(commentRange.pos, commentRange.end)
    .split("\n")
    .slice(1, -1) // Remove start and ending comments characters `/** */`
    .map((line) => line.replace("*", "").trim()) // Remove leading * and spaces
    .join("\n")

  return comment
}
