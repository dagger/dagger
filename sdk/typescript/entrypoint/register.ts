import {
  dag,
  Function_,
  FunctionWithArgOpts,
  ModuleID,
  TypeDef,
  TypeDefKind,
} from "../api/client.gen.js"
import { DaggerModule } from "../introspector/scanner/abtractions/module.js"
import {
  ConstructorTypeDef,
  FunctionArgTypeDef,
  FunctionTypedef,
  ListTypeDef,
  ObjectTypeDef,
  ScalarTypeDef,
  TypeDef as ScannerTypeDef,
} from "../introspector/scanner/typeDefs.js"

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
    })

    // Register all functions (methods) to this object
    Object.values(object.methods).forEach((method) => {
      typeDef = typeDef.withFunction(addFunction(method.typeDef))
    })

    // Register all fields that belong to this object
    Object.values(object.properties).forEach((field) => {
      if (field.isExposed) {
        typeDef = typeDef.withField(
          field.alias ?? field.name,
          addTypeDef(field.typeDef.typeDef),
          {
            description: field.description,
          },
        )
      }
    })

    if (object._constructor) {
      typeDef = typeDef.withConstructor(
        addConstructor(object._constructor.typeDef, typeDef),
      )
    }

    // Add it to the module object
    mod = mod.withObject(typeDef)
  })

  // Call ID to actually execute the registration
  return await mod.id()
}

/**
 * Bind a constructor to the given object.
 */
function addConstructor(
  constructor: ConstructorTypeDef,
  owner: TypeDef,
): Function_ {
  return dag.function_("", owner).with(addArg(constructor.args))
}

/**
 * Create a function in the Dagger API.
 */
function addFunction(fct: FunctionTypedef): Function_ {
  return dag
    .function_(fct.alias ?? fct.name, addTypeDef(fct.returnType))
    .withDescription(fct.description)
    .with(addArg(fct.args))
}

/**
 * Register all arguments in the function.
 */
function addArg(args: {
  [name: string]: FunctionArgTypeDef
}): (fct: Function_) => Function_ {
  return function (fct: Function_): Function_ {
    Object.values(args).forEach((arg) => {
      const opts: FunctionWithArgOpts = {
        description: arg.description,
      }

      if (arg.defaultValue) {
        opts.defaultValue = arg.defaultValue as string & { __JSON: never }
      }

      let typeDef = addTypeDef(arg.typeDef)
      if (arg.optional) {
        typeDef = typeDef.withOptional(true)
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
    default:
      return dag.typeDef().withKind(type.kind)
  }
}
