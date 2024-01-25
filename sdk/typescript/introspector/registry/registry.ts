/* eslint-disable @typescript-eslint/no-unused-vars */

/* eslint-disable @typescript-eslint/no-explicit-any */
import "reflect-metadata"

import { UnknownDaggerError } from "../../common/errors/UnknownDaggerError.js"

export type Class = { new (...args: any[]): any }

export type State = { [property: string]: any }

export type Args = Record<string, unknown>

/**
 * Datastructures that store the class constructor to allow invoking it
 * from the registry and store method's name.
 */
type RegistryClass = {
  class_: Class
  methods: string[]
}

/**
 * Registry stores class and method that have the @object decorator.
 *
 * This is a convenient way to make possible the invocation of class' function.
 *
 * The decorator @object store the class into the Registry, but also all the
 * users method's name.
 * It doesn't consider the `@func` or `field` decorators because this is
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
   *
   */
  object = <T extends Class>(constructor: T): T => {
    const methods: string[] = []

    // Create a dummy instance of the constructor to loop through its properties
    // We only register user's method and ignore TypeScript default method
    let proto = new constructor()
    while (proto && proto !== Object.prototype) {
      const ownMethods = Object.getOwnPropertyNames(proto).filter((name) => {
        const descriptor = Object.getOwnPropertyDescriptor(proto, name)

        // Check if the descriptor exist, then if it's a function and finally
        // if the function is owned by the class.
        return (
          descriptor &&
          typeof descriptor.value === "function" &&
          Object.prototype.hasOwnProperty.call(proto, name)
        )
      })

      methods.push(...ownMethods)

      proto = Object.getPrototypeOf(proto)
    }

    Reflect.defineMetadata(
      constructor.name,
      { class_: constructor, methods },
      this
    )

    return constructor
  }

  /**
   * The definition of @field decorator that should be on top of any
   * class' property that must be exposed to the Dagger API.
   */
  field = (target: object, propertyKey: string) => {
    // A placeholder to declare fields
  }

  /**
   * The definition of @func decorator that should be on top of any
   * class' method that must be exposed to the Dagger API.
   */
  func = (
    target: object,
    propertyKey: string | symbol,
    descriptor: PropertyDescriptor
  ) => {
    // The logic is done in the object constructor since it's not possible to
    // access the class parent's name from a method constructor without calling
    // the method itself
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
    inputs: Args
  ): Promise<any> {
    // Retrieve the resolver class from its key
    const resolver = Reflect.getMetadata(object, this) as RegistryClass
    if (!resolver) {
      throw new UnknownDaggerError(
        `${object} is not register as a resolver`,
        {}
      )
    }

    // If method is nil, apply the constructor.
    if (method === "") {
      return new resolver.class_(...Object.values(inputs))
    }

    // Safety check to make sure the method called exist in the class
    // to avoid the app to crash brutally.
    if (!resolver.methods.find((m) => m === method)) {
      throw new UnknownDaggerError(
        `${method} is not registered in the resolver ${object}`,
        {}
      )
    }

    // Instantiate the class
    let r = new resolver.class_() as any

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
