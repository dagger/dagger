/* eslint-disable @typescript-eslint/no-unused-vars */
/* eslint-disable @typescript-eslint/no-explicit-any */
// @experimentalDecorators
// @emitDecoratorMetadata
import "reflect-metadata"

import { UnknownDaggerError } from "../common/errors/index.js"

export type Class = { new (...args: any[]): any }

export type State = { [property: string]: any }

export type Args = Record<string, unknown>

/**
 * Datastructures that store the class constructor to allow invoking it
 * from the registry and store method's name.
 */
type RegistryClass = {
  class_: Class
}

export type ArgumentOptions = {
  /**
   * The contextual value to use for the argument.
   *
   * This should only be used for Directory/File or GitRepository/GitRef types.
   *
   * An absolute path would be related to the context source directory (the git repo root or the module source root).
   * A relative path would be relative to the module source root.
   */
  defaultPath?: string

  /**
   * Patterns to ignore when loading the contextual argument value.
   *
   * This should only be used for Directory types.
   */
  ignore?: string[]
}

export type FunctionOptions = {
  /**
   * The caching behavior of this function.
   * "never" means no caching.
   * "session" means caching only for the duration of the current client's session.
   * A duration string (e.g., "5m", "1h") means persistent caching for that duration.
   * By default, caching is enabled with a long default set by the engine.
   */
  cache?: string

  /**
   * An optional alias to use for the function when exposed on the API.
   */
  alias?: string
}

/**
 * Registry stores class and method that have the @object decorator.
 *
 * This is a convenient way to make possible the invocation of class' function.
 *
 * The decorator @object store the class into the Registry, but also all the
 * users method's name.
 * It doesn't consider the `@func` decorator because this is
 * used by the Dagger API to know what to expose or not.
 * This might lead to unnecessary data register into the registry, but
 * we use map as datastructure to optimize the searching process
 * since we directly look through a key into the `class_` member of
 * RegistryClass.
 */
export class Registry {
  /**
   * The definition of the @object decorator that should be on top of any
   * class module that must be exposed to the Dagger API.
   */
  object = (): (<T extends Class>(constructor: T) => T) => {
    return <T extends Class>(constructor: T): T => {
      Reflect.defineMetadata(constructor.name, { class_: constructor }, this)

      return constructor
    }
  }

  /**
   * The definition of the @enum decorator that should be on top of any
   * class module that must be exposed to the Dagger API as enumeration.
   *
   * @deprecated In favor of using TypeScript `enum` types.
   */
  enumType = (): (<T extends Class>(constructor: T) => T) => {
    return <T extends Class>(constructor: T): T => {
      return constructor
    }
  }

  /**
   * The definition of @field decorator that should be on top of any
   * class' property that must be exposed to the Dagger API.
   *
   * @deprecated In favor of `@func`
   * @param alias The alias to use for the field when exposed on the API.
   */
  field = (alias?: string): ((target: object, propertyKey: string) => void) => {
    return (target: object, propertyKey: string) => {
      // A placeholder to declare field in the registry.
    }
  }

  /**
   * The definition of @func decorator that should be on top of any
   * class' method that must be exposed to the Dagger API.
   */
  func = (
    alias?: string,
  ): ((
    target: object,
    propertyKey: string | symbol,
    descriptor?: PropertyDescriptor,
  ) => void) => {
    return (
      target: object,
      propertyKey: string | symbol,
      descriptor?: PropertyDescriptor,
    ) => {}
  }

  argument = (
    opts?: ArgumentOptions,
  ): ((
    target: object,
    propertyKey: string | undefined,
    parameterIndex: number,
  ) => void) => {
    return (
      target: object,
      propertyKey: string | undefined,
      parameterIndex: number,
    ) => {}
  }

  /**
   * Build a class that is part of the module itself so you can
   * access its sub functions.
   *
   * If there's no class associated, return the object itself.
   */
  buildClass(object: string, state: State): any {
    const resolver = Reflect.getMetadata(object, this) as RegistryClass
    if (!resolver) {
      return object
    }

    let r = Object.create(resolver.class_.prototype)
    r = Object.assign(r, state)

    return r
  }

  /**
   * getResult check for the object and method in the registry and call it
   * with the given input and state.
   *
   * This is the function responsible for any module methods execution.
   *
   * @param object The class to look for
   * @param method The method to call in the class
   * @param state The current state of the class
   * @param inputs The input to send to the method to call
   */
  async getResult(
    object: string,
    method: string,
    state: State,
    inputs: Args,
  ): Promise<any> {
    // Retrieve the resolver class from its key
    const resolver = Reflect.getMetadata(object, this) as RegistryClass
    if (!resolver) {
      throw new UnknownDaggerError(
        `${object} is not register as a resolver`,
        {},
      )
    }

    // If method is nil, apply the constructor.
    if (method === "") {
      return new resolver.class_(...Object.values(inputs))
    }

    // Instantiate the class without calling the constructor
    let r = Object.create(resolver.class_.prototype)

    // Safety check to make sure the method called exist in the class
    // to avoid the app to crash brutally.
    if (!r[method]) {
      throw new UnknownDaggerError(
        `${method} is not registered in the resolver ${object}`,
        {},
      )
    }

    // Apply state to the class
    r = Object.assign(r, state)

    // Execute and return the result
    return await r[method](...Object.values(inputs))
  }
}

/**
 * The default registry used in any module.
 */
export const registry = new Registry()
