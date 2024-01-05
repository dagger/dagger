/**
 * Expose the decorator publicly, so they insert data into the global registry.
 */
import { registry } from "../registry/registry.js"

export const object = registry.object
export const func = registry.func
export const field = registry.field
