/* eslint-disable @typescript-eslint/no-explicit-any */
import { ClientError, gql, GraphQLClient } from "graphql-request"

import {
  GraphQLRequestError,
  TooManyNestedObjectsError,
  UnknownDaggerError,
} from "../common/errors/index.js"
import { QueryTree } from "./client.gen.js"

/**
 * Format argument into a GraphQL compliant arguments format.
 */
function buildArgs(args: any): string {
  // Remove unwanted quotes
  const formatValue = (v: string) =>
    JSON.stringify(v).replace(/\{"[a-zA-Z]+"/gi, (str) => str.replace(/"/g, ""))

  const entries = Object.entries(args).reduce((acc: any, cur) => {
    const [key, value] = cur

    if (value) {
      acc.push(`${key}: ${formatValue(value as string)}`)
    }

    return acc
  }, [])

  if (entries.length === 0) {
    return ""
  }

  return `(${entries})`
}

/**
 * Find QueryTree, convert them into GraphQl query
 * then compute and return the result to the appropriate field
 */
async function computeNestedQuery(
  query: QueryTree[],
  client: GraphQLClient
): Promise<void> {
  /**
   * Check if there is a nested queryTree to be executed
   */
  const isQueryTree = (value: any) => value["_queryTree"] !== undefined

  // Remove all undefined args and assert args is defined
  const queryToExec = query.filter((q): q is Required<QueryTree> => !!q.args)

  for (const q of queryToExec) {
    await Promise.all(
      Object.entries(q.args).map(async ([key, value]: any) => {
        if (value instanceof Object && isQueryTree(value)) {
          // push an id that will be used by the container
          const getQueryTree = buildQuery([
            ...value["_queryTree"],
            {
              operation: "id",
            },
          ])

          q.args[key] = await compute(getQueryTree, client)
        }
      })
    )
  }
}

/**
 * Convert the queryTree into a GraphQL query
 * @param q
 * @returns
 */
export function buildQuery(q: QueryTree[]): string {
  const query = q.reduce((acc, { operation, args }, i) => {
    const qLen = q.length

    acc += ` ${operation} ${args ? `${buildArgs(args)}` : ""} ${
      qLen - 1 !== i ? "{" : "}".repeat(qLen - 1)
    }`

    return acc
  }, "")

  return `{${query} }`
}

/**
 * Convert QueryTree into a Graphql query then compute it
 *
 * @param q | QueryTree[]
 * @param client | GraphQLClient
 * @returns
 */
export async function queryBuilder<T>(
  q: QueryTree[],
  client: GraphQLClient
): Promise<T> {
  await computeNestedQuery(q, client)

  const query = buildQuery(q)

  return await compute(query, client)
}

/**
 * Return a Graphql query result flattened
 * @param response any
 * @returns
 */
export function queryFlatten<T>(response: any): T {
  // Recursion break condition
  // If our response is not an object or an array we assume we reached the value
  if (!(response instanceof Object) || Array.isArray(response)) {
    return response
  }

  const keys = Object.keys(response)

  if (keys.length != 1) {
    // Dagger is currently expecting to only return one value
    // If the response is nested in a way were more than one object is nested inside throw an error
    throw new TooManyNestedObjectsError(
      "Too many nested objects inside graphql response",
      { response: response }
    )
  }

  const nestedKey = keys[0]

  return queryFlatten(response[nestedKey])
}

/**
 * Send a GraphQL document to the server
 * return a flatted result
 * @hidden
 */
export async function compute<T>(
  query: string,
  client: GraphQLClient
): Promise<T> {
  let computeQuery: Awaited<T>

  try {
    computeQuery = await client.request(
      gql`
        ${query}
      `
    )
  } catch (e: any) {
    if (e instanceof ClientError) {
      throw new GraphQLRequestError("Error message", {
        request: e.request,
        response: e.response,
        cause: e,
      })
    }

    // Just throw the unknown error
    throw new UnknownDaggerError(
      "Encountered an unknown error while requesting data via graphql",
      { cause: e as Error }
    )
  }

  return queryFlatten(computeQuery)
}
