export { GraphQLClient } from "graphql-request"

// Default client bindings
export * from "./api/client.gen.js"

// Common errors
export * from "./common/errors/index.js"

// Connection for library
export type { CallbackFct } from "./connect.js"
export { connect, connection } from "./connect.js"
export type { ConnectOpts } from "./connectOpts.js"

// Export dagger connection context
export { Context } from "./common/context.js"

// Module library
export * from "./module/decorators.js"
export { entrypoint } from "./module/entrypoint/entrypoint.js"
