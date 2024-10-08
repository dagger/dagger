/* eslint-disable @typescript-eslint/no-explicit-any */

/* eslint-disable @typescript-eslint/no-unused-vars */

/**
 * Expose the decorator publicly, so they insert data into the global registry.
 */
export type Class = { new (...args: any[]): any }

/**
 * The definition of the `@object` decorator that should be on top of any
 * class module that must be exposed to the Dagger API.
 *
 */
export const object = (): (<T extends Class>(constructor: T) => T) => {
  return <T extends Class>(constructor: T): T => {
    return constructor
  }
}

/**
 * The definition of @func decorator that should be on top of any
 * class' method that must be exposed to the Dagger API.
 *
 * @param alias The alias to use for the field when exposed on the API.
 */
export const func = (
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

/**
 * The definition of @field decorator that should be on top of any
 * class' property that must be exposed to the Dagger API.
 *
 * @deprecated In favor of `@func`
 * @param alias The alias to use for the field when exposed on the API.
 */
export const field = (
  alias?: string,
): ((target: object, propertyKey: string) => void) => {
  return (target: object, propertyKey: string) => {
    // A placeholder to declare field in the registry.
  }
}

/**
 * The definition of the `@enumType` decorator that should be on top of any
 * class module that must be exposed to the Dagger API as enumeration.
 */
export const enumType = (): (<T extends Class>(constructor: T) => T) => {
  return <T extends Class>(constructor: T): T => {
    return constructor
  }
}

export type ArgumentOptions = {
  /**
   * The contextual value to use for the argument.
   *
   * This should only be used for Directory or File types.
   *
   * An abslute path would be related to the context source directory (the git repo root or the module source root).
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

/**
 * Add a `@argument` decorator to an argument of type `Directory` or `File` to load
 * its contents from the module context directory.
 *
 * The context directory is the git repository containing the module.
 * If there's no git repository, the context directory is the directory containing
 * the module source code.
 *
 * @param opts.defaultPath Only applies to arguments of type File or Directory. If the argument is not set,
 * load it from the given path in the context directory.
 * @param opts.ignore Only applies to arguments of type Directory. The ignore patterns are applied to the input directory,
 * and matching entries are filtered out, in a cache-efficient manner..
 *
 * Relative paths are relative to the current source files.
 * Absolute paths are rooted to the module context directory.
 */
export const argument = (
  opts?: ArgumentOptions,
): ((target: object, propertyKey: string, parameterIndex: number) => void) => {
  return (target: object, propertyKey: string, parameterIndex: number) => {}
}
