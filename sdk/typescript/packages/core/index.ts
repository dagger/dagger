/**
 * The `@dagger.io/core` entry point: the generated bindings, `dag`, and the
 * typed `connect`.
 *
 * Published from this repo because its version is the engine release: the
 * bindings beside this file describe one engine's schema.
 */
export * from "./client.gen.js"
export { connect } from "./connect.js"
export type { CallbackFct } from "./connect.js"
