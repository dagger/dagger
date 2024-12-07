import * as opentelemetry from "@opentelemetry/api"
import { GraphQLClient } from "graphql-request"

import { Client } from "./api/client.gen.js"
import { Context } from "./common/context.js"
import { withGQLClient } from "./common/graphql/connect.js"
import { Connection, globalConnection } from "./common/graphql/connection.js"
import { ConnectOpts } from "./connectOpts.js"
import * as telemetry from "./telemetry/telemetry.js"

export type CallbackFct = (client: Client) => Promise<void>

/**
 * connection executes the given function using the default global Dagger client.
 *
 * @example
 * ```ts
 * await connection(
 *   async () => {
 *     await dag
 *       .container()
 *       .from("alpine")
 *       .withExec(["apk", "add", "curl"])
 *       .withExec(["curl", "https://dagger.io/"])
 *       .sync()
 *   }, { LogOutput: process.stderr }
 * )
 * ```
 */
export async function connection(
  fct: () => Promise<void>,
  cfg: ConnectOpts = {},
) {
  try {
    telemetry.initialize()

    // Wrap connection into the opentelemetry context for propagation
    await opentelemetry.context.with(telemetry.getContext(), async () => {
      try {
        await withGQLClient(cfg, async (gqlClient) => {
          // Set the GQL client inside the global dagger client
          globalConnection.setGQLClient(gqlClient)

          await fct()
        })
      } finally {
        globalConnection.resetClient()
      }
    })
  } finally {
    await telemetry.close()
  }
}

/**
 * connect runs GraphQL server and initializes a
 * GraphQL client to execute query on it through its callback.
 * This implementation is based on the existing Go SDK.
 */
export async function connect(
  cb: CallbackFct,
  config: ConnectOpts = {},
): Promise<void> {
  await withGQLClient(config, async (gqlClient: GraphQLClient) => {
    const connection = new Connection(gqlClient)
    const ctx = new Context([], connection)
    const client = new Client(ctx)

    // Warning shall be throw if versions are not compatible
    try {
      await client.version()
    } catch (e) {
      console.error("failed to check version compatibility:", e)
    }

    return await cb(client)
  })
}
