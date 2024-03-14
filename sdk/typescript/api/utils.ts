/* eslint-disable @typescript-eslint/no-explicit-any */
import { ClientError, gql, GraphQLClient } from "graphql-request"

import {
  GraphQLRequestError,
  TooManyNestedObjectsError,
  UnknownDaggerError,
  NotAwaitedRequestError,
  ExecError,
} from "../common/errors/index.js"
import { Metadata, QueryTree } from "./client.gen.js"

/**
 * Format argument into GraphQL query format.
 */
function buildArgs(args: any): string {
  const metadata: Metadata = args.__metadata || {}

  // Remove unwanted quotes
  const formatValue = (key: string, value: string) => {
    // Special treatment for enumeration, they must be inserted without quotes
    if (metadata[key]?.is_enum) {
      return JSON.stringify(value).replace(/['"]+/g, "")
    }

    return JSON.stringify(value).replace(
      /\{"[a-zA-Z]+":|,"[a-zA-Z]+":/gi,
      (str) => {
        return str.replace(/"/g, "")
      },
    )
  }

  if (args === undefined || args === null) {
    return ""
  }

  const formattedArgs = Object.entries(args).reduce(
    (acc: any, [key, value]) => {
      // Ignore internal metadata key
      if (key === "__metadata") {
        return acc
      }

      if (value !== undefined && value !== null) {
        acc.push(`${key}: ${formatValue(key, value as string)}`)
      }

      return acc
    },
    [],
  )

  if (formattedArgs.length === 0) {
    return ""
  }

  return `(${formattedArgs})`
}

/**
 * Find QueryTree, convert them into GraphQl query
 * then compute and return the result to the appropriate field
 */
async function computeNestedQuery(
  query: QueryTree[],
  client: GraphQLClient,
): Promise<void> {
  // Check if there is a nested queryTree to be executed
  const isQueryTree = (value: any) => value["_queryTree"] !== undefined

  // Check if there is a nested array of queryTree to be executed
  const isArrayQueryTree = (value: any[]) =>
    value.every((v) => v instanceof Object && isQueryTree(v))

  // Prepare query tree for final query by computing nested queries
  // and building it with their results.
  const computeQueryTree = async (value: any): Promise<string> => {
    // Resolve sub queries if operation's args is a subquery
    for (const op of value["_queryTree"]) {
      await computeNestedQuery([op], client)
    }

    // push an id that will be used by the container
    return buildQuery([
      ...value["_queryTree"],
      {
        operation: "id",
      },
    ])
  }

  // Remove all undefined args and assert args type
  const queryToExec = query.filter((q): q is Required<QueryTree> => !!q.args)

  for (const q of queryToExec) {
    await Promise.all(
      // Compute nested query for single object
      Object.entries(q.args).map(async ([key, value]: any) => {
        if (value instanceof Object && isQueryTree(value)) {
          // push an id that will be used by the container
          const getQueryTree = await computeQueryTree(value)

          q.args[key] = await compute(getQueryTree, client)
        }

        // Compute nested query for array of object
        if (Array.isArray(value) && isArrayQueryTree(value)) {
          const tmp: any = q.args[key]

          for (let i = 0; i < value.length; i++) {
            // push an id that will be used by the container
            const getQueryTree = await computeQueryTree(value[i])

            tmp[i] = await compute(getQueryTree, client)
          }

          q.args[key] = tmp
        }
      }),
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
 * Convert querytree into a Graphql query then compute it
 * @param q | QueryTree[]
 * @param client | GraphQLClient
 * @returns
 */
export async function computeQuery<T>(
  q: QueryTree[],
  client: GraphQLClient,
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
      {
        response: response,
      },
    )
  }

  const nestedKey = keys[0]

  return queryFlatten(response[nestedKey])
}

/**
 * Send a GraphQL document to the server
 * return a flatten result
 * @hidden
 */
export async function compute<T>(
  query: string,
  client: GraphQLClient,
): Promise<T> {
  let computeQuery: Awaited<T>
  try {
    computeQuery = await client.request(gql`
      ${query}
    `)
  } catch (e: any) {
    if (e instanceof ClientError) {
      const msg = e.response.errors?.[0]?.message ?? `API Error`
      const ext = e.response.errors?.[0]?.extensions

      if (ext?._type === "EXEC_ERROR") {
        throw new ExecError(msg, {
          cmd: (ext.cmd as string[]) ?? [],
          exitCode: (ext.exitCode as number) ?? -1,
          stdout: (ext.stdout as string) ?? "",
          stderr: (ext.stderr as string) ?? "",
        })
      }

      throw new GraphQLRequestError(msg, {
        request: e.request,
        response: e.response,
        cause: e,
      })
    }

    // Looking for connection error in case the function has not been awaited.
    if (e.errno === "ECONNREFUSED") {
      throw new NotAwaitedRequestError(
        "Encountered an error while requesting data via graphql through a synchronous call. Make sure the function called is awaited.",
        { cause: e },
      )
    }

    // Just throw the unknown error
    throw new UnknownDaggerError(
      "Encountered an unknown error while requesting data via graphql",
      {
        cause: e,
      },
    )
  }

  return queryFlatten(computeQuery)
}
