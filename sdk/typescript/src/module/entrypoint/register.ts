import {
  dag,
  Function_,
  FunctionWithArgOpts,
  ModuleID,
  TypeDef,
  TypeDefKind,
  SourceMap,
} from "../../api/client.gen.js"
import {
  DaggerArguments as Arguments,
  DaggerConstructor as Constructor,
  DaggerFunction as Method,
  DaggerModule,
  Locatable,
} from "../introspector/dagger_module/index.js"
import { DaggerInterfaceFunction } from "../introspector/dagger_module/interfaceFunction.js"
import {
  EnumTypeDef,
  InterfaceTypeDef,
  ListTypeDef,
  ObjectTypeDef,
  ScalarTypeDef,
  TypeDef as ScannerTypeDef,
} from "../introspector/typedef.js"

/**
 * Register the module files and returns its ID
 */
export async function register(
  files: string[],
  module: DaggerModule,
): Promise<ModuleID> {
  // Get a new module that we will fill in with all the types
  let mod = dag.module_()

  // Add module description if any.
  if (module.description) {
    mod = mod.withDescription(module.description)
  }

  // For each class scanned, register its type, method and properties in the module.
  Object.values(module.objects).forEach((object) => {
    // Register the class Typedef object in Dagger
    let typeDef = dag.typeDef().withObject(object.name, {
      description: object.description,
      sourceMap: addSourceMap(object),
    })

    // Register all functions (methods) to this object
    Object.values(object.methods).forEach((method) => {
      typeDef = typeDef.withFunction(addFunction(method))
    })

    // Register all fields that belong to this object
    Object.values(object.properties).forEach((field) => {
      if (field.isExposed) {
        typeDef = typeDef.withField(
          field.alias ?? field.name,
          addTypeDef(field.type!),
          {
            description: field.description,
            sourceMap: addSourceMap(field),
          },
        )
      }
    })

    if (object._constructor) {
      typeDef = typeDef.withConstructor(
        addConstructor(object._constructor, typeDef),
      )
    }

    // Add it to the module object
    mod = mod.withObject(typeDef)
  })

  // Register all enums defined by this modules
  Object.values(module.enums).forEach((enum_) => {
    let typeDef = dag.typeDef().withEnum(enum_.name, {
      description: enum_.description,
      sourceMap: addSourceMap(enum_),
    })

    Object.values(enum_.values).forEach((value) => {
      typeDef = typeDef.withEnumValue(value.value, {
        description: value.description,
        sourceMap: addSourceMap(value),
      })
    })

    mod = mod.withEnum(typeDef)
  })

  // Register all interfaces defined by this module
  Object.values(module.interfaces).forEach((interface_) => {
    let typeDef = dag.typeDef().withInterface(interface_.name, {
      description: interface_.description,
    })

    Object.values(interface_.functions).forEach((function_) => {
      typeDef = typeDef.withFunction(addFunction(function_))
    })

    mod = mod.withInterface(typeDef)
  })

  // Call ID to actually execute the registration
  return await mod.id()
}

/**
 * Bind a constructor to the given object.
 */
function addConstructor(constructor: Constructor, owner: TypeDef): Function_ {
  return dag.function_("", owner).with(addArg(constructor.arguments))
}

/**
 * Create a function in the Dagger API.
 */
function addFunction(fct: Method | DaggerInterfaceFunction): Function_ {
  return dag
    .function_(fct.alias ?? fct.name, addTypeDef(fct.returnType!))
    .withDescription(fct.description)
    .withSourceMap(addSourceMap(fct))
    .with(addArg(fct.arguments))
}

/**
 * Register all arguments in the function.
 */
function addArg(args: Arguments): (fct: Function_) => Function_ {
  return function (fct: Function_): Function_ {
    Object.values(args).forEach((arg) => {
      const opts: FunctionWithArgOpts = {
        description: arg.description,
        sourceMap: addSourceMap(arg),
      }

      let typeDef = addTypeDef(arg.type!)
      if (arg.isOptional) {
        typeDef = typeDef.withOptional(true)
      }

      // Check if both values are used, return an error if so.
      if (arg.defaultValue && arg.defaultPath) {
        throw new Error(
          "cannot set both default value and default path from context",
        )
      }

      // We do not set the default value if it's not a primitive type, we let TypeScript
      // resolve the default value during the runtime instead.
      // If it has a default value but is not primitive, we set the value as optional
      // to workaround the fact that the API isn't aware of the default value and will
      // expect it to be set as required input.
      if (arg.defaultValue !== undefined) {
        if (isPrimitiveType(arg.type!)) {
          opts.defaultValue = JSON.stringify(arg.defaultValue) as string & {
            __JSON: never
          }
        } else {
          typeDef = typeDef.withOptional(true)
        }
      }

      // If the argument is a contextual argument, it becomes optional.
      if (arg.defaultPath) {
        opts.defaultPath = arg.defaultPath
      }

      if (arg.ignore) {
        opts.ignore = arg.ignore
      }

      fct = fct.withArg(arg.name, typeDef, opts)
    })

    return fct
  }
}

/**
 * Wrapper around TypeDef to return the right Dagger TypesDef with its options.
 *
 * This function only convert the Typedef into correct dagger call
 * but, it's up to function above with more context to add documentation,
 * define if it's an optional value or its default's.
 *
 * We cannot do it there because the Typedef can come from any source:
 * a field, a param, a return value etc...
 */
function addTypeDef(type: ScannerTypeDef<TypeDefKind>): TypeDef {
  switch (type.kind) {
    case TypeDefKind.ScalarKind:
      return dag.typeDef().withScalar((type as ScalarTypeDef).name)
    case TypeDefKind.ObjectKind:
      return dag.typeDef().withObject((type as ObjectTypeDef).name)
    case TypeDefKind.ListKind:
      return dag.typeDef().withListOf(addTypeDef((type as ListTypeDef).typeDef))
    case TypeDefKind.VoidKind:
      return dag.typeDef().withKind(type.kind).withOptional(true)
    case TypeDefKind.EnumKind:
      return dag.typeDef().withEnum((type as EnumTypeDef).name)
    case TypeDefKind.InterfaceKind:
      return dag.typeDef().withInterface((type as InterfaceTypeDef).name)
    default:
      return dag.typeDef().withKind(type.kind)
  }
}

function addSourceMap(object: Locatable): SourceMap {
  const { filepath, line, column } = object.getLocation()

  return dag.sourceMap(filepath, line, column)
}

function isPrimitiveType(type: ScannerTypeDef<TypeDefKind>): boolean {
  return (
    type.kind === TypeDefKind.BooleanKind ||
    type.kind === TypeDefKind.IntegerKind ||
    type.kind === TypeDefKind.StringKind ||
    type.kind === TypeDefKind.FloatKind ||
    type.kind === TypeDefKind.EnumKind
  )
}
