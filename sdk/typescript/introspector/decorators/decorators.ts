/**
 * Expose the decorator publicly, so they insert data into the global registry.
 */
import { registry } from "../registry/registry.js"

export const object = registry.object
export const func = registry.func
/**
 * @deprecated In favor of `@func`
 */
export const field = registry.field
export const enumType = registry.enumType
