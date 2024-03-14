import ts from "typescript"

/**
 * Convert the TypeScript type from the compiler API into a readable textual
 * type.
 *
 * @param checker The typescript compiler checker.
 * @param type The type to convert.
 */
export function serializeType(checker: ts.TypeChecker, type: ts.Type): string {
  const strType = checker.typeToString(type)

  // Remove Promise<> wrapper around type if it's a promise.
  if (strType.startsWith("Promise")) {
    return strType.substring("Promise<".length, strType.length - 1)
  }

  return strType
}
