import * as opentelemetry from "@opentelemetry/api"
import { GraphQLClient } from "graphql-request"

const createFetchWithTimeout =
  (timeout: number) => async (input: RequestInfo | URL, init?: RequestInit) => {
    if (init?.signal) {
      throw new Error(
        "it looks like graphql-request started using AbortSignal on its own. Please check graphql-request's recent updates",
      )
    }

    const controller = new AbortController()

    const timerId = setTimeout(() => {
      controller.abort()
    }, timeout)

    try {
      return await fetch(input, { ...init, signal: controller.signal })
    } finally {
      clearTimeout(timerId)
    }
  }

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
    fetch: createFetchWithTimeout(1000 * 60 * 30), // 30minutes timeout
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
