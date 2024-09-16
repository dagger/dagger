import * as opentelemetry from "@opentelemetry/api"
import { GraphQLClient } from "graphql-request"

/**
 * Customer setter to inject trace parent into the request headers
 * This is required because `graphql-request` 7.0.1 changes its header
 * type to `Headers` which break the default open telemetry propagator.
 */
class CustomSetter {
  set(carrier: Headers, key: string, value: string): void {
    carrier.set(key, value)
  }
}

export function createGQLClient(port: number, token: string): GraphQLClient {
  const client = new GraphQLClient(`http://127.0.0.1:${port}/query`, {
    headers: {
      Authorization: "Basic " + Buffer.from(token + ":").toString("base64"),
    },
    // Inject trace parent into the request headers so it can be correctly linked
    requestMiddleware: async (req) => {
      opentelemetry.propagation.inject(
        opentelemetry.context.active(),
        req.headers,
        new CustomSetter(),
      )

      return req
    },
  })

  return client
}
