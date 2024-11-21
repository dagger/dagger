/* eslint-disable @typescript-eslint/no-explicit-any */
import Module from "node:module"

<<<<<<<< HEAD:sdk/typescript/src/module/executor.ts
import { FunctionNotFound } from "../common/errors/index.js"
import { DaggerModule } from "./introspector/dagger_module/index.js"
========
import { FunctionNotFound } from "../../common/errors/index.js"
import { DaggerModule } from "../introspector/dagger_module/index.js"
>>>>>>>> cb4a2c412 (feat: decouple provisioning & refactor ts sdk architecture):sdk/typescript/src/module/executor/index.ts

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
      throw new FunctionNotFound(
        `Object ${object} not found in the introspection`,
      )
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
