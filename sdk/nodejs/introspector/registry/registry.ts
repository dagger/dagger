import { UnknownDaggerError } from "../../common/errors/UnknownDaggerError.js"

export type Class = { new (...args: unknown[]): unknown }

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type State = { [property: string]: any }

type RegistryClass = {
  [key: string]: {
    class_: Class
    methods: string[]
  }
}

export class Registry {
  private classes: RegistryClass = {}

  object = <T extends Class>(constructor: T): T => {
    const methods: string[] = []

    // Create a dummy instance of the constructor to loop through its properties
    // We only register user's method and ignore Typescript default method
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

    // Add this to the registry
    this.classes[constructor.name] = { class_: constructor, methods }

    return constructor
  }

  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  field = (target: object, propertyKey: string) => {
    // A placeholder to declare fields
  }

  func = (
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    target: object,
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    propertyKey: string | symbol,
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    descriptor: PropertyDescriptor
  ) => {
    // The logic is done in the object constructor since it's not possible to
    // access the class parent's name from a method constructor without calling
    // the method itself
  }

  async getResult(
    object: string,
    method: string,
    state: State,
    inputs: unknown[]
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ): Promise<any> {
    const resolver = this.classes[object]

    if (!resolver) {
      throw new UnknownDaggerError(
        `${object} is not register as a resolver`,
        {}
      )
    }

    if (!resolver.methods.find((m) => m === method)) {
      throw new UnknownDaggerError(
        `${method} is not registered in the resolver ${object}`,
        {}
      )
    }

    // Create the class
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    let r = new resolver.class_() as any

    // Apply state to the object
    r = Object.assign(r, state)

    // Execute and return the result
    return await r[method](...inputs)
  }
}

export const registry = new Registry()
