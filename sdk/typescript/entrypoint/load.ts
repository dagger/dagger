/* eslint-disable @typescript-eslint/no-explicit-any */
import { dag, TypeDefKind } from "../api/client.gen.js"
import { ScanResult } from "../introspector/scanner/scan.js"
import { TypeDef } from "../introspector/scanner/typeDefs.js"

/**
 * Import all given typescript files so that trigger their decorators
 * and register their class and functions inside the Registry.
 *
 * @param files List of files to load.
 */
export async function load(files: string[]): Promise<void> {
  await Promise.all(files.map(async (f) => await import(f)))
}

/**
 * Load the order of arguments for a given function from the scan result.
 *
 * @param scanResult The result of the scan.
 * @param parentName The name of the class.
 * @param fnName The name of the function.
 *
 * @returns An array of strings representing the order of arguments.
 */
export function loadArgOrder(
  scanResult: ScanResult,
  parentName: string,
  fnName: string
): string[] {
  const classTypeDef = scanResult.classes[parentName]
  if (!classTypeDef) {
    throw new Error(`could not find class ${parentName}`)
  }

  // Call for the constructor
  if (fnName === "") {
    return Object.keys(classTypeDef.constructor?.args ?? {})
  }

  const methodTypeDef = classTypeDef.methods[fnName]
  if (!methodTypeDef) {
    throw new Error(`could not find method ${fnName}`)
  }

  return Object.keys(methodTypeDef.args)
}

/**
 * Load the argument for the given function and check if it's variadic.
 *
 * @param scanResult The result of the scan.
 * @param parentName The name of the class.
 * @param fnName The name of the function.
 * @param argName The name of the argument.
 *
 * @returns True if the argument is variadic, false otherwise.
 */
export function isArgVariadic(
  scanResult: ScanResult,
  parentName: string,
  fnName: string,
  argName: string
): boolean {
  const classTypeDef = scanResult.classes[parentName]
  if (!classTypeDef) {
    throw new Error(`could not find class ${parentName}`)
  }

  // It's not possible to have variadic arguments in the constructor.
  if (fnName === "") {
    return false
  }

  const methodTypeDef = classTypeDef.methods[fnName]
  if (!methodTypeDef) {
    throw new Error(`could not find method ${fnName}`)
  }

  return methodTypeDef.args[argName].isVariadic
}

/**
 * Load the argument type from the scan result.
 *
 * @param scanResult Result of the scan
 * @param parentName Class called
 * @param fnName Function called
 * @param argName Argument name
 * @returns The type of the argument
 */
export function loadArgType(
  scanResult: ScanResult,
  parentName: string,
  fnName: string,
  argName: string
): TypeDef<TypeDefKind> {
  const classTypeDef = scanResult.classes[parentName]
  if (!classTypeDef) {
    throw new Error(`could not find class ${parentName}`)
  }

  // Call for the constructor
  if (fnName === "") {
    const argTypeDef = classTypeDef.constructor?.args[argName]
    if (!argTypeDef) {
      throw new Error(`could not find argument ${argName} type in constructor`)
    }

    return argTypeDef.typeDef
  }

  const methodTypeDef = classTypeDef.methods[fnName]
  if (!methodTypeDef) {
    throw new Error(`could not find method ${fnName}`)
  }

  const argTypeDef = methodTypeDef.args[argName]
  if (!argTypeDef) {
    throw new Error(`could not find argument ${argName} type`)
  }

  return argTypeDef.typeDef
}

/**
 * Load the property type from the scan result.
 *
 * @param scanResult Result of the scan
 * @param parentName Class called
 * @param propertyName property of the class
 * @returns the type of the property
 */
export function loadPropertyType(
  scanResult: ScanResult,
  parentName: string,
  propertyName: string
): TypeDef<TypeDefKind> {
  const classTypeDef = scanResult.classes[parentName]
  if (!classTypeDef) {
    throw new Error(`could not find class ${parentName}`)
  }

  const propertyTypeDef = classTypeDef.fields[propertyName]
  if (!propertyTypeDef) {
    throw new Error(`could not find property ${propertyName} type`)
  }

  return propertyTypeDef.typeDef
}

/**
 * Load the true name from the scan result
 *
 * @param scanResult Result of the scan
 * @param parentName Class called
 * @param alias exposed name
 * @param kind location of the alias
 */
export function loadName(
  scanResult: ScanResult,
  parentName: string,
  alias: string,
  kind: "field" | "function" | "object"
): string {
  const classTypeDef = scanResult.classes[parentName]
  if (!classTypeDef) {
    throw new Error(`could not find class ${parentName}`)
  }

  switch (kind) {
    case "object": {
      return classTypeDef.name
    }
    case "function": {
      // Handle constructor
      if (alias === "") {
        return ""
      }

      const methodTypeDef = classTypeDef.methods[alias]
      if (!methodTypeDef) {
        throw new Error(`could not find method ${alias} type`)
      }

      return methodTypeDef.name
    }
    case "field": {
      const propertyTypeDef = classTypeDef.fields[alias]
      if (!propertyTypeDef) {
        throw new Error(`could not find property ${alias}`)
      }

      return propertyTypeDef.name
    }
  }
}

/**
 * Load the alias from the true property name.
 * If not found, return the original alias because it's not
 * a registered field.
 *
 * @param scanResult Result of the scan
 * @param parentName Class called
 * @param alias exposed name
 */
export function loadResultAlias(
  scanResult: ScanResult,
  parentName: string,
  alias: string
): string {
  const classTypeDef = scanResult.classes[parentName]
  if (!classTypeDef) {
    return alias
  }

  const fieldTypeDef = Object.values(classTypeDef.fields).find(
    (field) => field.name === alias
  )
  if (!fieldTypeDef) {
    return alias
  }

  return fieldTypeDef.alias ?? fieldTypeDef.name
}

/**
 * Return the eventual parent name of a field if its return type is a
 * registered object.
 * If not found, return the original parent name.
 *
 * @param scanResult The result of the scan.
 * @param parentName Original parent name
 * @param alias The field alias
 */
export function loadAliasParentName(
  scanResult: ScanResult,
  parentName: string,
  alias: string
): string {
  const classTypeDef = scanResult.classes[parentName]
  if (!classTypeDef) {
    return parentName
  }

  const fieldTypeDef = Object.values(classTypeDef.fields).find(
    (field) => field.name === alias
  )
  if (!fieldTypeDef) {
    return parentName
  }

  if (fieldTypeDef.typeDef.kind === TypeDefKind.ObjectKind) {
    return (fieldTypeDef.typeDef as TypeDef<TypeDefKind.ObjectKind>).name
  }

  return parentName
}

/**
 * This function load the argument as a Dagger type.
 *
 * Note: The JSON.parse() is required to remove extra quotes
 */
export async function loadArg(
  value: any,
  type: TypeDef<TypeDefKind>
): Promise<any> {
  // If value is undefinied, return it directly.
  if (value === undefined) {
    return value
  }

  switch (type.kind) {
    case TypeDefKind.ListKind:
      return Promise.all(
        value.map(
          async (v: any) =>
            await loadArg(v, (type as TypeDef<TypeDefKind.ListKind>).typeDef)
        )
      )
    case TypeDefKind.ObjectKind: {
      const objectType = (type as TypeDef<TypeDefKind.ObjectKind>).name

      // Workaround to call get any object that has an id
      // eslint-disable-next-line @typescript-eslint/ban-ts-comment
      // @ts-ignore
      if (dag[`load${objectType}FromID`]) {
        // eslint-disable-next-line @typescript-eslint/ban-ts-comment
        // @ts-ignore
        return dag[`load${objectType}FromID`](value)
      }

      // TODO(supports subfields serialization)
      return value
    }
    // Cannot use , to specify multiple matching case so instead we use fallthrough.
    case TypeDefKind.StringKind:
    case TypeDefKind.IntegerKind:
    case TypeDefKind.BooleanKind:
    case TypeDefKind.VoidKind:
      return value
    default:
      throw new Error(`unsupported type ${type.kind}`)
  }
}

/**
 * Load subfields of the result and IDable object.
 *
 * @param result The result of the invocation.
 * @param scanResult The result of the scan.
 * @param parentName The name of the parent object.
 * @returns Loaded result.
 */
export async function loadResult(
  result: any,
  scanResult: ScanResult,
  parentName: string
): Promise<any> {
  if (result && typeof result?.id === "function") {
    result = await result.id()
  }

  if (typeof result === "object") {
    for (const [key, value] of Object.entries(result)) {
      result[loadResultAlias(scanResult, parentName, key)] = await loadResult(
        value,
        scanResult,
        loadAliasParentName(scanResult, parentName, key)
      )
    }
  }

  return result
}
