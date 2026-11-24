import {
  Connection,
  ConnectOpts,
  Context,
  withGQLClient,
} from "@dagger.io/session"
import { Client } from "./client.gen.js"

export type CallbackFct = (client: Client) => Promise<void>

/**
 * connect runs GraphQL server and initializes a
 * GraphQL client to execute query on it through its callback.
 * This implementation is based on the existing Go SDK.
 */
export async function connect(
  cb: CallbackFct,
  config: ConnectOpts = {},
): Promise<void> {
  await withGQLClient(config, async (gqlClient) => {
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
