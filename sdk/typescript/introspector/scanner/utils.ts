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

/**
 * Check if the class is the main object of the module.
 *
 * @param classtName The name of the class to check.
 * @param moduleName The name of the module.
 */
export function isMainObject(className: string, moduleName: string): boolean {
  const toPascalCase = (input: string): string => {
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

  return toPascalCase(moduleName) === className
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
 * Return true if the given property has the decorator @field() on top
 * of its declaration.
 *
 * @param property The property to check
 */
export function isField(property: ts.PropertyDeclaration): boolean {
  return (
    ts.getDecorators(property)?.find((d) => {
      if (ts.isCallExpression(d.expression)) {
        return d.expression.expression.getText() === "field"
      }

      return false
    }) !== undefined
  )
}

export function getAlias(
  elem: ts.HasDecorators,
  kind: "field" | "func"
): string | undefined {
  const decorator = ts.getDecorators(elem)?.find((d) => {
    if (ts.isCallExpression(d.expression)) {
      return d.expression.expression.getText() === kind
    }

    return false
  })

  if (!decorator) {
    return undefined
  }

  const expression = decorator.expression as ts.CallExpression
  const args = expression.arguments

  const alias = args[0]?.getText()
  if (alias) {
    return JSON.parse(alias.replace(/'/g, '"'))
  }

  return undefined
}

/**
 * Return true if the given property is public.
 *
 * This function actually in work the reverse, it checks if the property
 * isn't private nor protected.
 *
 * It returns true if the property has no modifiers since no keyword
 * has been set on the property.
 *
 * Example
 * ```
 * class Human {
 *     private age = 22 // Return false
 *     protected familyName = "Doe" // Return false
 *
 *     @field
 *     name = "John" // Return true
 *
 *     city = "Paris" // Return false because there's no decorator
 * }
 * ```
 *
 * @param property The property to check on.
 */
export function isPublicProperty(property: ts.PropertyDeclaration): boolean {
  if (!isField(property)) {
    return false
  }

  const modifiers = ts.getModifiers(property)
  if (!modifiers) {
    return true
  }

  return !modifiers.some(
    (modifier) =>
      modifier.kind === ts.SyntaxKind.PrivateKeyword ||
      modifier.kind === ts.SyntaxKind.ProtectedKeyword
  )
}

type OptionalValue = {
  optional: boolean
  defaultValue?: string
}

/**
 * Return true if the parameter is optional.
 *
 * This includes both optional value defines with `?` and value that
 * have a default value.
 *
 * If there's a default value, its expression is returned in the result.
 *
 * @param param The param to check.
 */
export function isOptional(param: ts.Symbol): OptionalValue {
  const result: OptionalValue = { optional: false }

  const declarations = param.getDeclarations()

  // Only check if the parameters actually have declarations
  if (declarations && declarations.length > 0) {
    const parameterDeclaration = declarations[0]

    // Convert the symbol declaration into Parameter
    if (ts.isParameter(parameterDeclaration)) {
      result.optional =
        parameterDeclaration.questionToken !== undefined ||
        parameterDeclaration.initializer !== undefined

      if (parameterDeclaration.initializer !== undefined) {
        result.defaultValue = formatDefaultValue(
          parameterDeclaration.initializer.getText()
        )
      }
    }
  }

  return result
}

export function isVariadic(param: ts.Symbol): boolean {
  const declarations = param.getDeclarations()

  // Only check if the parameters actually have declarations
  if (declarations && declarations.length > 0) {
    const parameterDeclaration = declarations[0]

    // Convert the symbol declaration into Parameter
    if (ts.isParameter(parameterDeclaration)) {
      return parameterDeclaration.dotDotDotToken !== undefined
    }
  }

  return false
}

function formatDefaultValue(value: string): string {
  const isSingleQuoteString = (): boolean =>
    value.startsWith("'") && value.endsWith("'")

  if (isSingleQuoteString()) {
    return `"${value.slice(1, value.length - 1)}"`
  }

  return value
}

/**
 * Convert a typename into a Dagger Typedef using dynamic typing.
 */
export function typeNameToTypedef(typeName: string): TypeDef<TypeDefKind> {
  // If it's a list, remove the '[]' and recall the function to get
  // the type of list
  if (typeName.endsWith("[]")) {
    return {
      kind: TypeDefKind.ListKind,
      typeDef: typeNameToTypedef(typeName.slice(0, typeName.length - 2)),
    }
  }

  switch (typeName) {
    case "string":
      return { kind: TypeDefKind.StringKind }
    case "number":
      return { kind: TypeDefKind.IntegerKind }
    case "boolean":
      return { kind: TypeDefKind.BooleanKind }
    case "void":
      return { kind: TypeDefKind.VoidKind }
    default:
      return {
        kind: TypeDefKind.ObjectKind,
        name: typeName,
      }
  }
}
