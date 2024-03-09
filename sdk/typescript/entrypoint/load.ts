/* eslint-disable @typescript-eslint/no-explicit-any */
import { dag, TypeDefKind } from "../api/client.gen.js"
import { TypeDef } from "../introspector/scanner/typeDefs.js"
import { InvokeCtx } from "./context.js"
import { DaggerModule } from "../introspector/scanner/abtractions/module.js"
import { Method } from "../introspector/scanner/abtractions/method.js"
import { Constructor } from "../introspector/scanner/abtractions/constructor.js"
import { DaggerObject } from "../introspector/scanner/abtractions/object.js"
import { Args } from "../introspector/registry/registry.js"

/**
 * Import all given typescript files so that trigger their decorators
 * and register their class and functions inside the Registry.
 *
 * @param files List of files to load.
 */
export async function load(files: string[]): Promise<void> {
  await Promise.all(files.map(async (f) => await import(f)))
}

export function loadInvokedObject(module: DaggerModule, parentName: string): DaggerObject {
  return module.objects[parentName]
}

export async function loadParentState(object: DaggerObject, ctx: InvokeCtx): Promise<Args> {
  const parentState: Args = {}

  for (const [key, value] of Object.entries(ctx.parentArgs)) {
    const property = object.properties[key]
    if (!property) {
      throw new Error(`could not find parent property ${key}`)
    }

    parentState[property.name] = await loadValue(value, property.type)
  }

  return parentState
}

export function loadInvokedMethod(
  object: DaggerObject,
  ctx: InvokeCtx,
): (Method | Constructor) | undefined {
  if (ctx.fnName === "") {
    return object._constructor
  }

  return object.methods[ctx.fnName]
}

export async function loadArgs(method: Method | Constructor, ctx: InvokeCtx): Promise<Args> {
  const args: Args = {}

  // Load arguments
  for (const argName of method.getArgOrder()) {
    const argument = method.arguments[argName]
    if (!argument) {
      throw new Error(`could not find argument ${argName}`)
    }

    const loadedArg = await loadValue(ctx.fnArgs[argName], argument.type)

    // If the argument is variadic, we need to load each args independently
    // so it's correctly propagated when it's sent to the function.
    // Note: variadic args are always last in the list of args.
    if (argument.isVariadic) {
      for (const [i, arg] of (loadedArg ?? []).entries()) {
        args[`${argName}${i}`] = arg
      }

      continue
    }

    // If the argument is nullable and the loaded arg is undefined with no default value, we set it to null.
    if (argument.isNullable && loadedArg === undefined && !argument.defaultValue) {
      args[argName] = null
      continue
    }

    args[argName] = loadedArg
  }

  return args
}

/**
 * This function load the value as a Dagger type.
 *
 * Note: The JSON.parse() is required to remove extra quotes
 */
export async function loadValue(value: any, type: TypeDef<TypeDefKind>): Promise<any> {
  // If value is undefinied, return it directly.
  if (value === undefined) {
    return value
  }

  switch (type.kind) {
    case TypeDefKind.ListKind:
      return Promise.all(
        value.map(
          async (v: any) => await loadValue(v, (type as TypeDef<TypeDefKind.ListKind>).typeDef),
        ),
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

export async function loadResult(
  result: any,
  module: DaggerModule,
  object: DaggerObject,
): Promise<any> {
  // Handle IDable objects
  if (result && typeof result?.id === "function") {
    result = await result.id()
  }

  // Handle arrays
  if (Array.isArray(result)) {
    result = await Promise.all(result.map(async (r) => await loadResult(r, module, object)))

    return result
  }

  // Handle objects
  if (typeof result === "object") {
    const state: any = {}

    for (const [key, value] of Object.entries(result)) {
      const property = Object.values(object.properties).find((p) => p.name === key)
      if (!property) {
        throw new Error(`could not find result property ${key}`)
      }

      if (property.type.kind === TypeDefKind.ObjectKind) {
        const referencedObject =
          module.objects[(property.type as TypeDef<TypeDefKind.ObjectKind>).name]
        if (referencedObject) {
          object = referencedObject
        }
      }

      state[property.alias ?? property.name] = await loadResult(value, module, object)
    }

    return state
  }

  // Handle primitive types
  return result
}
