/* eslint-disable @typescript-eslint/no-explicit-any */
import { gql, GraphQLClient } from "graphql-request"
import { QueryTree } from "./client.gen.js"

function buildArgs(item: any): string {
  const entries = Object.entries(item.args)
    .filter((value) => value[1] !== undefined)
    .map((value) => {
      return `${value[0]}: ${JSON.stringify(value[1]).replace(
        /\{"[a-zA-Z]+"/gi,
        (str) => str.replace(/"/g, "")
      )}`
    })
  if (entries.length === 0) {
    return ""
  }
  return "(" + entries + ")"
}

/**
 * Find querytree, convert them into GraphQl query
 * then compute and return the result to the appropriate field
 */
async function computeNestedQuery(
  query: QueryTree[],
  client: GraphQLClient
): Promise<void> {
  /**
   * Check if there is a nested queryTree to be executed
   */
  const isQueryTree = (value: any) =>
    Object.keys(value).find((val) => val === "_queryTree")

  for (const q of query) {
    if (q.args !== undefined) {
      await Promise.all(
        Object.entries(q.args).map(async (val: any) => {
          if (val[1] instanceof Object && isQueryTree(val[1])) {
            // push an id that will be used by the container
            const getQueryTree = buildQuery([
              ...val[1]["_queryTree"],
              {
                operation: "id",
              },
            ])
            const result = await compute(getQueryTree, client)
            // eslint-disable-next-line @typescript-eslint/ban-ts-comment
            //@ts-ignore
            q.args[val[0]] = result
          }
        })
      )
    }
  }
}

/**
 * Convert the queryTree into a GraphQL query
 * @param q
 * @returns
 */
export function buildQuery(q: QueryTree[]): string {
  let query = "{"
  q.forEach((item: QueryTree, index: number) => {
    query += `
        ${item.operation} ${item.args ? `${buildArgs(item)}` : ""} ${
      q.length - 1 !== index ? "{" : "}".repeat(q.length - 1)
    }
      `
  })
  query += "}"

  return query
}

/**
 * Convert querytree into a Graphql query then compute it
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

  const result: Awaited<T> = await compute(query, client)

  return result
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
    // TODO Throw sensible Error
    throw new Error("Too many Graphql nested objects")
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
  client: GraphQLClient
): Promise<T> {
  const computeQuery: Awaited<T> = await client.request(
    gql`
      ${query}
    `
  )

  return queryFlatten(computeQuery)
}
