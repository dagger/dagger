/* eslint-disable @typescript-eslint/no-explicit-any */
import { dag, TypeDefKind } from "../api/client.gen.js"
import { Args, Registry } from "../introspector/registry/registry.js"
import { Constructor } from "../introspector/scanner/abtractions/constructor.js"
import { DaggerEnum } from "../introspector/scanner/abtractions/enum.js"
import { Method } from "../introspector/scanner/abtractions/method.js"
import { DaggerModule } from "../introspector/scanner/abtractions/module.js"
import { DaggerObject } from "../introspector/scanner/abtractions/object.js"
import { TypeDef } from "../introspector/scanner/typeDefs.js"
import { InvokeCtx } from "./context.js"

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
 * Return the object invoked from the module.
 *
 * @param module The module to load the object from.
 * @param parentName The name of the parent object.
 */
export function loadInvokedObject(
  module: DaggerModule,
  parentName: string,
): DaggerObject {
  return module.objects[parentName]
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

/**
 * Load the values of the arguments from the context.
 *
 * @param method Method to load the arguments from.
 * @param ctx The context of the invocation.
 */
export async function loadArgs(
  registry: Registry,
  method: Method | Constructor,
  ctx: InvokeCtx,
): Promise<Args> {
  const args: Args = {}

  // Load arguments
  for (const argName of method.getArgOrder()) {
    const argument = method.arguments[argName]
    if (!argument) {
      throw new Error(`could not find argument ${argName}`)
    }

    const loadedArg = await loadValue(
      registry,
      ctx.fnArgs[argName],
      argument.type,
    )

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
    if (
      argument.isNullable &&
      loadedArg === undefined &&
      !argument.defaultValue
    ) {
      args[argName] = null
      continue
    }

    args[argName] = loadedArg
  }

  return args
}

/**
 * Load the state of the parent object from the context.
 *
 * @param object The object to load the parent state from.
 * @param ctx The context of the invocation.
 */
export async function loadParentState(
  registry: Registry,
  object: DaggerObject,
  ctx: InvokeCtx,
): Promise<Args> {
  const parentState: Args = {}

  for (const [key, value] of Object.entries(ctx.parentArgs)) {
    const property = object.properties[key]
    if (!property) {
      throw new Error(`could not find parent property ${key}`)
    }

    parentState[property.name] = await loadValue(registry, value, property.type)
  }

  return parentState
}

/**
 * This function load the value as a Dagger type.
 *
 * Note: The JSON.parse() is required to remove extra quotes
 */
export async function loadValue(
  registry: Registry,
  value: any,
  type: TypeDef<TypeDefKind>,
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
            await loadValue(
              registry,
              v,
              (type as TypeDef<TypeDefKind.ListKind>).typeDef,
            ),
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
      return registry.buildClass(objectType, value)
    }
    // Cannot use `,` to specify multiple matching case so instead we use fallthrough.
    case TypeDefKind.StringKind:
    case TypeDefKind.IntegerKind:
    case TypeDefKind.BooleanKind:
    case TypeDefKind.VoidKind:
    case TypeDefKind.ScalarKind:
    case TypeDefKind.EnumKind:
      return value
    default:
      throw new Error(`unsupported type ${type.kind}`)
  }
}

/**
 * Load the object type from the return type of the method.
 * This covers the case where the return type is an other object of the module.
 * For example: `msg(): Message` where message is an object of the module.
 *
 * @param module  The module to load the object from.
 * @param object The current object to load the return type from.
 * @param method The method to load the return type from.
 */
export function loadObjectReturnType(
  module: DaggerModule,
  object: DaggerObject,
  method: Method,
): DaggerObject | DaggerEnum {
  const retType = method.returnType

  switch (retType.kind) {
    case TypeDefKind.ListKind: {
      // Loop until we find the original object type.
      // This way we handle the list of list (e.g Object[][][]...[])
      let listType = retType
      while (listType.kind === TypeDefKind.ListKind) {
        listType = (listType as TypeDef<TypeDefKind.ListKind>).typeDef
      }

      return module.objects[(listType as TypeDef<TypeDefKind.ObjectKind>).name]
    }
    case TypeDefKind.ObjectKind:
      return module.objects[(retType as TypeDef<TypeDefKind.ObjectKind>).name]
    case TypeDefKind.EnumKind:
      return module.enums[(retType as TypeDef<TypeDefKind.EnumKind>).name]
    default:
      return object
  }
}

export async function loadResult(
  result: any,
  module: DaggerModule,
  object: DaggerObject | DaggerEnum,
): Promise<any> {
  // Handle IDable objects
  if (result && typeof result?.id === "function") {
    result = await result.id()
  }

  // Handle arrays
  if (Array.isArray(result)) {
    result = await Promise.all(
      result.map(async (r) => await loadResult(r, module, object)),
    )

    return result
  }

  // Handle objects
  if (typeof result === "object" && object instanceof DaggerObject) {
    const state: any = {}

    for (const [key, value] of Object.entries(result)) {
      const property = Object.values(object.properties).find(
        (p) => p.name === key,
      )
      if (!property) {
        throw new Error(`could not find result property ${key}`)
      }

      let referencedObject: DaggerObject | undefined = undefined

      // Handle nested objects
      if (property.type.kind === TypeDefKind.ObjectKind) {
        referencedObject =
          module.objects[
            (property.type as TypeDef<TypeDefKind.ObjectKind>).name
          ]
      }

      // Handle list of nested objects
      if (property.type.kind === TypeDefKind.ListKind) {
        let _property = property.type

        // Loop until we find the original type.
        while (_property.kind === TypeDefKind.ListKind) {
          _property = (_property as TypeDef<TypeDefKind.ListKind>).typeDef
        }

        // If the original type is an object, we use it as the referenced object.
        if (_property.kind === TypeDefKind.ObjectKind) {
          referencedObject =
            module.objects[(_property as TypeDef<TypeDefKind.ObjectKind>).name]
        }
      }

      // If there's no referenced object, we use the current object.
      if (!referencedObject) {
        referencedObject = object
      }

      state[property.alias ?? property.name] = await loadResult(
        value,
        module,
        referencedObject,
      )
    }

    return state
  }

  if (typeof result === "object" && object instanceof DaggerEnum) {
    return result
  }

  // Handle primitive types
  return result
}
