import ts from "typescript"

import { TypeDefKind } from "../../api/client.gen.js"
import { TypeDef } from "./typeDefs.js"

/**
 * Return true if the given class declaration has the decorator @obj on
 * top of its declaration.
 * @param object
 */
export function isObject(object: ts.ClassDeclaration): boolean {
  return (
    ts
      .getDecorators(object)
      ?.find((d) => d.expression.getText() === "object") !== undefined
  )
}

/**
 * Return true if the given method has the decorator @fct on top
 * of its declaration.
 *
 * @param method The method to check
 */
export function isFunction(method: ts.MethodDeclaration): boolean {
  return (
    ts.getDecorators(method)?.find((d) => d.expression.getText() === "func") !==
    undefined
  )
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
  const decorators = ts.getDecorators(property)
  if (!decorators) {
    return false
  }

  if (
    decorators.find((d) => d.expression.getText() === "field") === undefined
  ) {
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
      kind: TypeDefKind.Listkind,
      typeDef: typeNameToTypedef(typeName.slice(0, typeName.length - 2)),
    }
  }

  switch (typeName) {
    case "string":
      return { kind: TypeDefKind.Stringkind }
    case "number":
      return { kind: TypeDefKind.Integerkind }
    case "boolean":
      return { kind: TypeDefKind.Booleankind }
    case "void":
      return { kind: TypeDefKind.Voidkind }
    default:
      return {
        kind: TypeDefKind.Objectkind,
        name: typeName,
      }
  }
}
