import { GraphQLClient } from "graphql-request"

import { ConnectOpts } from "../../connectOpts.js"
import { createGQLClient } from "./client.js"

/**
 * Execute the callback with a GraphQL client connected to the Dagger engine.
 * It automatically provisions the engine if needed.
 */
export async function withGQLClient<T>(
  connectOpts: ConnectOpts,
  cb: (gqlClient: GraphQLClient) => Promise<T>,
): Promise<T> {
  const nesting = process.env["DAGGER_NESTING"]
  const port = process.env["DAGGER_SESSION_PORT"]
  if (
    nesting !== undefined &&
    nesting !== "" &&
    nesting !== "NESTED_CLIENT" &&
    nesting !== "INDEPENDENT_SESSIONS"
  ) {
    throw new Error(`unknown DAGGER_NESTING value ${JSON.stringify(nesting)}`)
  }
  if (
    (nesting === "NESTED_CLIENT" || nesting === "INDEPENDENT_SESSIONS") &&
    !port
  ) {
    throw new Error(`DAGGER_NESTING=${nesting} requires DAGGER_SESSION_PORT`)
  }
  if (nesting === "INDEPENDENT_SESSIONS" && port) {
    if (!Number.isInteger(Number(port)) || Number(port) < 1) {
      throw new Error(
        `invalid DAGGER_SESSION_PORT value ${JSON.stringify(port)}`,
      )
    }
  } else if (port) {
    if (!process.env["DAGGER_SESSION_TOKEN"]) {
      throw new Error(
        "DAGGER_SESSION_TOKEN must be set if DAGGER_SESSION_PORT is set",
      )
    }

    const token = process.env["DAGGER_SESSION_TOKEN"]

    return await cb(createGQLClient(Number(port), token))
  }

  try {
    const provisioning = await import("../../provisioning/index.js")

    return await provisioning.withEngineSession(connectOpts, cb)
  } catch (e) {
    throw new Error(
      `failed to execute function with automatic provisioning: ${e}`,
      { cause: e },
    )
  }
}
