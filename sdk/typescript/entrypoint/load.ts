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
 * Load the argument order from the scan result.
 *
 * @param scanResult Result of the scan
 * @param parentName Class called
 * @param fnName Function called
 * @returns The order of the arguments
 */
export function loadArgOrder(
  scanResult: ScanResult,
  parentName: string,
  fnName: string
): string[] {
  const classTypeDef = scanResult.classes.find((c) => c.name === parentName)
  if (!classTypeDef) {
    throw new Error(`could not find class ${parentName}`)
  }

  const methodTypeDef = classTypeDef.methods.find((m) => m.name === fnName)
  if (!methodTypeDef) {
    throw new Error(`could not find method ${fnName}`)
  }

  return methodTypeDef.args.map((a) => a.name)
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
  const classTypeDef = scanResult.classes.find((c) => c.name === parentName)
  if (!classTypeDef) {
    throw new Error(`could not find class ${parentName}`)
  }

  const methodTypeDef = classTypeDef.methods.find((m) => m.name === fnName)
  if (!methodTypeDef) {
    throw new Error(`could not find method ${fnName}`)
  }

  const argTypeDef = methodTypeDef.args.find((a) => a.name === argName)
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
  const classTypeDef = scanResult.classes.find((c) => c.name === parentName)
  if (!classTypeDef) {
    throw new Error(`could not find class ${parentName}`)
  }

  const propertyTypeDef = classTypeDef.fields.find(
    (p) => p.name === propertyName
  )
  if (!propertyTypeDef) {
    throw new Error(`could not find property ${propertyName} type`)
  }

  return propertyTypeDef.typeDef
}

/**
 * This function load the argument as a Dagger type.
 *
 * Note: The JSON.parse() is required to remove extra quotes
 */
export async function loadArg(
  value: string,
  type: TypeDef<TypeDefKind>
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
): Promise<any> {
  switch (type.kind) {
    case TypeDefKind.ListKind:
      return JSON.parse(value)
    case TypeDefKind.ObjectKind: {
      const objectType = (type as TypeDef<TypeDefKind.ObjectKind>).name

      // This ID might be wrapped in quotes depending on the source.
      // We need to remove them to be able to call the loading function.
      if (value.startsWith('"') && value.endsWith('"')) {
        value = JSON.parse(value)
      }

      // Workaround to call get any object that has an id
      // eslint-disable-next-line @typescript-eslint/ban-ts-comment
      // @ts-ignore
      return dag[`load${objectType}FromID`](value)
    }
    case TypeDefKind.StringKind:
      return JSON.parse(value)
    case TypeDefKind.IntegerKind:
      return Number(value)
    case TypeDefKind.BooleanKind:
      return value === "true" // Return false if string is different than true
    case TypeDefKind.InterfaceKind:
      throw new Error("interface not supported")
    case TypeDefKind.VoidKind:
      return null
  }
}
