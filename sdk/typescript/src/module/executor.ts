/* eslint-disable @typescript-eslint/no-explicit-any */
/* eslint-disable @typescript-eslint/ban-ts-comment */
import Module from "node:module"

import { dag, TypeDefKind } from "../api/client.gen.js"
import { Context } from "../common/context.js"
import { FunctionNotFound } from "../common/errors/index.js"
import { Connection } from "../common/graphql/connection.js"
import { DaggerModule } from "./introspector/dagger_module/index.js"
import { DaggerInterfaceFunctions } from "./introspector/dagger_module/interfaceFunction.js"
import { TypeDef } from "./introspector/typedef.js"

export type State = { [property: string]: any }

export type Args = Record<string, unknown>

export class Executor {
  constructor(
    public readonly modules: Module[],
    private readonly daggerModule: DaggerModule,
  ) {}

  private getExportedObject(object: string): any {
    const key = object as keyof Module
    const module = this.modules.find((m) => m[key] !== undefined)
    if (!module) {
      throw new FunctionNotFound(`Object ${object} not found`)
    }

    return module[key]
  }

  buildClass(object: string, state: State): any {
    const daggerObject = this.daggerModule.objects[object]
    if (!daggerObject) {
      throw new FunctionNotFound(`Object ${object} not found in the module`)
    }

    switch (daggerObject.kind()) {
      case "class": {
        const obj = this.getExportedObject(object)

        const instanciatedClass = Object.create(obj.prototype)
        Object.assign(instanciatedClass, state)

        return instanciatedClass
      }
      case "object": {
        return state
      }
    }
  }

  /**
   * Transform a Dagger interface identifier into an implementation of this
   * interface serves as module call binding to reach the true implementation.
   */
  buildInterface(iface: string, id: string): any {
    const interfaceObject = this.daggerModule.interfaces[iface]
    if (!interfaceObject) {
      throw new Error(`Interface ${iface} not found in the module`)
    }

    const ifaceImpl = new InterfaceWrapper(
      this,
      this.daggerModule,
      `${this.daggerModule.name}${iface}`,
      id,
      interfaceObject.functions,
    )

    return ifaceImpl
  }

  async getResult(
    object: string,
    method: string,
    state: State,
    inputs: Args,
  ): Promise<any> {
    const obj = this.getExportedObject(object)

    if (method === "") {
      return new obj(...Object.values(inputs))
    }

    const builtObj = this.buildClass(object, state)
    if (!builtObj[method]) {
      throw new FunctionNotFound(`Method ${method} not found`)
    }

    return await builtObj[method](...Object.values(inputs))
  }
}

/**
 * Interface Wrapper serves as dynaminc module binding so the module can
 * call function of this interface.
 * Because the actual interface implementation can come from any external modules,
 * all resolution are done by API Call.
 *
 * @example
 * ```ts
 * interface Example {
 *   foo: () => Promise<string>
 * }
 *
 * class Test {
 *   @func()
 *   async callFoo(example: Example): Promise<string> {
 *     // <- This example argument here is actually the Interface Wrapper, and `foo` will,
 *     // directly execute the API call to reach the given implementation.
 *     return example.foo()
 *   }
 * }
 * ```
 */
class InterfaceWrapper {
  private _ctx: Context

  constructor(
    private readonly executor: Executor,
    private readonly module: DaggerModule,
    private readonly ifaceName: string,
    private readonly ifaceId: string,
    private readonly fcts: DaggerInterfaceFunctions,
  ) {
    this._ctx = new Context([], new Connection(dag.getGQLClient()))

    // Load the interface by its identifier
    this._ctx = this._ctx.select(`load${ifaceName}FromID`, { id: ifaceId })

    Object.entries(fcts).forEach(([name, fct]) => {
      const argKeys = Object.keys(fct.arguments)

      // Dynamically adding functions of the interface and it's resolvers.
      // @ts-ignore
      this[name] = async (...args: any[]) => {
        // Fill up arguments of that function.
        const argsPayload = {}
        for (let i = 0; i < argKeys.length; i++) {
          if (args[i] !== undefined) {
            // @ts-ignore
            argsPayload[argKeys[i]] = args[i]
          }
        }

        this._ctx = this._ctx.select(name, argsPayload)

        // If the function is returning an IDable, we don't need to execute it
        // since it will be resolved later.
        if (
          fct.returnType!.kind === TypeDefKind.InterfaceKind ||
          fct.returnType!.kind === TypeDefKind.ObjectKind
        ) {
          return this
        }

        // If the function is returning a list, we may need to load the sub-objects
        if (fct.returnType!.kind === TypeDefKind.ListKind) {
          const listTypeDef = (fct.returnType as TypeDef<TypeDefKind.ListKind>)
            .typeDef

          // If the list is an object or an interface, then we need to load the sub-objects.
          if (
            listTypeDef.kind === TypeDefKind.ObjectKind ||
            listTypeDef.kind === TypeDefKind.InterfaceKind
          ) {
            const typedef = listTypeDef as
              | TypeDef<TypeDefKind.ObjectKind>
              | TypeDef<TypeDefKind.InterfaceKind>

            // Resolves the call to get the list of IDs to load
            const ids = await this._ctx
              .select("id")
              .execute<Array<{ id: string }>>()

            // If the return type is an interface defined by that module, we need to load it through
            // the interface wrapper
            if (this.module.interfaces[typedef.name]) {
              return await Promise.all(
                ids.map(
                  ({ id }) =>
                    new InterfaceWrapper(
                      this.executor,
                      module,
                      `${this.module.name}${typedef.name}`,
                      id,
                      this.module.interfaces[typedef.name].functions,
                    ),
                ),
              )
            }

            // Otherwise, we can just load the objects from the API
            return await Promise.all(
              // @ts-ignore
              ids.map(({ id }) => dag[`load${listTypeDef.name}FromID`](id)),
            )
          }
        }

        return await this._ctx.execute()
      }
    })
  }

  /**
   * ID function to make the interface IDeable when serialized as return value to the
   * Dagger API.
   */
  public async id(): Promise<string> {
    const ctx = this._ctx.select("id")

    return await ctx.execute()
  }
}
