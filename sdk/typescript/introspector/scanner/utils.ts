import ts from "typescript"

import { TypeDefKind } from "../../api/client.gen.js"
import { TypeDef } from "./typeDefs.js"

/**
 * Return true if the given class declaration has the decorator @obj() on
 * top of its declaration.
 * @param object
 */
export function isObject(object: ts.ClassDeclaration): boolean {
  return (
    ts.getDecorators(object)?.find((d) => {
      if (ts.isCallExpression(d.expression)) {
        return d.expression.expression.getText() === "object"
      }

      return false
    }) !== undefined
  )
}

export function toPascalCase(input: string): string {
  const words = input
    .replace(/[^a-zA-Z0-9]/g, " ") // Replace non-alphanumeric characters with spaces
    .split(/\s+/)
    .filter((word) => word.length > 0)

  if (words.length === 0) {
    return "" // No valid words found
  }

  // It's an edge case when moduleName is already in PascalCase or camelCase
  if (words.length === 1) {
    return words[0].charAt(0).toUpperCase() + words[0].slice(1)
  }

  const pascalCase = words
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
    .join("")

  return pascalCase
}

/**
 * Return true if the given method has the decorator @fct() on top
 * of its declaration.
 *
 * @param method The method to check
 */
export function isFunction(method: ts.MethodDeclaration): boolean {
  return (
    ts.getDecorators(method)?.find((d) => {
      if (ts.isCallExpression(d.expression)) {
        return d.expression.expression.getText() === "func"
      }

      return false
    }) !== undefined
  )
}

/**
 * Convert a type into a Dagger Typedef using dynamic typing.
 */
export function typeToTypedef(
  checker: ts.TypeChecker,
  type: ts.Type,
): TypeDef<TypeDefKind> {
  if (type.symbol?.name === "Promise") {
    const typeArgs = checker.getTypeArguments(type as ts.TypeReference)
    if (typeArgs.length > 0) {
      return typeToTypedef(checker, typeArgs[0])
    }
  }

  if (type.symbol?.name === "Array") {
    const typeArgs = checker.getTypeArguments(type as ts.TypeReference)
    if (typeArgs.length === 0) {
      throw new Error("Generic array not supported")
    }
    return {
      kind: TypeDefKind.ListKind,
      typeDef: typeToTypedef(checker, typeArgs[0]),
    }
  }

  if (type.symbol?.name && type.isClassOrInterface()) {
    return {
      kind: TypeDefKind.ObjectKind,
      name: type.symbol.name,
    }
  }

  const strType = checker.typeToString(type)

  switch (strType) {
    case "string":
      return { kind: TypeDefKind.StringKind }
    case "number":
      return { kind: TypeDefKind.IntegerKind }
    case "boolean":
      return { kind: TypeDefKind.BooleanKind }
    case "void":
      return { kind: TypeDefKind.VoidKind }
    default:
      // If it's a union, then it's a scalar type
      if (type.isUnionOrIntersection()) {
        return {
          kind: TypeDefKind.ScalarKind,
          name: strType,
        }
      }

      throw new Error(`Unsupported type ${strType}`)
  }
}
