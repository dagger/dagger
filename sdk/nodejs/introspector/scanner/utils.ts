import ts from "typescript"

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
        result.defaultValue = parameterDeclaration.initializer.getText()
      }
    }
  }

  return result
}
