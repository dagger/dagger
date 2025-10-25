import {
  dag,
  Function_,
  FunctionWithArgOpts,
  ModuleID,
  TypeDef,
  TypeDefKind,
  SourceMap,
  FunctionCachePolicy,
  FunctionWithCachePolicyOpts,
} from "../../api/client.gen.js"
import {
  DaggerArguments as Arguments,
  DaggerConstructor as Constructor,
  DaggerFunction as Method,
  DaggerModule,
  Locatable,
  DaggerArgument,
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

export class Register {
  constructor(private readonly module: DaggerModule) {}

  /**
   * Register the module in the engine and returns its ID
   */
  async run(): Promise<ModuleID> {
    // Get a new module that we will fill in with all the types
    let mod = dag.module_()

    // Add module description if any.
    if (this.module.description) {
      mod = mod.withDescription(this.module.description)
    }

    // For each class scanned, register its type, method and properties in the module.
    Object.values(this.module.objects).forEach((object) => {
      // Register the class Typedef object in Dagger
      let typeDef = dag.typeDef().withObject(object.name, {
        description: object.description,
        sourceMap: addSourceMap(object),
      })

      // Register all functions (methods) to this object
      Object.values(object.methods).forEach((method) => {
        typeDef = typeDef.withFunction(this.addFunction(method))
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
          this.addConstructor(object._constructor, typeDef),
        )
      }

      // Add it to the module object
      mod = mod.withObject(typeDef)
    })

    // Register all enums defined by this modules
    Object.values(this.module.enums).forEach((enum_) => {
      let typeDef = dag.typeDef().withEnum(enum_.name, {
        description: enum_.description,
        sourceMap: addSourceMap(enum_),
      })

      Object.values(enum_.values).forEach((value) => {
        typeDef = typeDef.withEnumMember(value.name, {
          value: value.value,
          description: value.description,
          sourceMap: addSourceMap(value),
        })
      })

      mod = mod.withEnum(typeDef)
    })

    // Register all interfaces defined by this module
    Object.values(this.module.interfaces).forEach((interface_) => {
      let typeDef = dag.typeDef().withInterface(interface_.name, {
        description: interface_.description,
      })

      Object.values(interface_.functions).forEach((function_) => {
        typeDef = typeDef.withFunction(this.addFunction(function_))
      })

      mod = mod.withInterface(typeDef)
    })

    return await mod.id()
  }

  /**
   * Bind a constructor to the given object.
   */
  addConstructor(constructor: Constructor, owner: TypeDef): Function_ {
    return dag.function_("", owner).with(this.addArg(constructor.arguments))
  }

  /**
   * Create a function in the Dagger API.
   */
  addFunction(fct: Method | DaggerInterfaceFunction): Function_ {
    let fnDef = dag
      .function_(fct.alias ?? fct.name, addTypeDef(fct.returnType!))
      .withDescription(fct.description)
      .withSourceMap(addSourceMap(fct))
      .with(this.addArg(fct.arguments))
    switch (fct.cache) {
      case "never": {
        fnDef = fnDef.withCachePolicy(FunctionCachePolicy.Never)
        break
      }
      case "session": {
        fnDef = fnDef.withCachePolicy(FunctionCachePolicy.PerSession)
        break
      }
      case "": {
        break
      }
      default: {
        const opts: FunctionWithCachePolicyOpts = { timeToLive: fct.cache }
        fnDef = fnDef.withCachePolicy(FunctionCachePolicy.Default, opts)
      }
    }

    return fnDef
  }

  /**
   * Register all arguments in the function.
   */
  addArg(args: Arguments): (fct: Function_) => Function_ {
    return (fct: Function_): Function_ => {
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
        if ([arg.defaultValue, arg.defaultPath].filter((v) => v).length > 1) {
          throw new Error("cannot set multiple defaults")
        }

        // We do not set the default value if it's not a primitive type, we let TypeScript
        // resolve the default value during the runtime instead.
        // If it has a default value but is not primitive, we set the value as optional
        // to workaround the fact that the API isn't aware of the default value and will
        // expect it to be set as required input.
        if (arg.defaultValue !== undefined) {
          const defaultValue = this.getDefaultValueFromArg(arg)
          if (defaultValue === undefined) {
            typeDef = typeDef.withOptional(true)
          } else {
            opts.defaultValue = JSON.stringify(defaultValue) as string & {
              __JSON: never
            }
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
   * Extract the default value from the argument.
   * - If the argument isn't a primitive type, we can't resolve it so we make it optional and let the runtime
   *   resolve it on execution.
   * - If the argument is a primitive type but not an enum, we resolve it directly.
   * - If the argument is an enum but not registered in the module, it must be a core enum so we keep the value as is.
   * - If the argument is an enum registered in the module, we resolve the value to the member name.
   */
  getDefaultValueFromArg(arg: DaggerArgument): unknown | undefined {
    if (!isPrimitiveType(arg.type!)) {
      return undefined
    }

    if (arg.type!.kind !== TypeDefKind.EnumKind) {
      return arg.defaultValue
    }

    const enumObj = this.module.enums[(arg.type! as EnumTypeDef).name]
    if (!enumObj) {
      // If the enum isn't found in the module, it may be a core enum so we keep the value
      return arg.defaultValue
    }

    // If it's a known enum, we need to resolve set the name of the member as default value instead of the actual value
    const enumMember = Object.entries(enumObj.values).find(
      ([, member]) => member.value === arg.defaultValue,
    )

    if (!enumMember) {
      throw new Error(
        `could not resolve default value '${arg.defaultValue}' for enum ${(arg.type! as EnumTypeDef).name}`,
      )
    }

    return enumMember[0]
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
