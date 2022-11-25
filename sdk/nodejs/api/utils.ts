/* eslint-disable @typescript-eslint/no-explicit-any */
import { QueryTree } from "./client.gen.js"

export function queryBuilder(q: QueryTree[]) {
  const args = (item: any) => {
    const regex = /\{"[a-zA-Z]+"/gi

    const entries = Object.entries(item.args)
      .filter((value) => value[1] !== undefined)
      .map((value) => {
        if (typeof value[1] === "object") {
          return `${value[0]}: ${JSON.stringify(value[1]).replace(
            regex,
            (str) => str.replace(/"/g, "")
          )}`
        }
        if (typeof value[1] === "number") {
          return `${value[0]}: ${value[1]}`
        }
        return `${value[0]}: "${value[1]}"`
      })
    if (entries.length === 0) {
      return ""
    }
    return "(" + entries + ")"
  }

  let query = "{"
  q.forEach((item: QueryTree, index: number) => {
    query += `
        ${item.operation} ${item.args ? `${args(item)}` : ""} ${
      q.length - 1 !== index ? "{" : "}".repeat(q.length - 1)
    }
      `
  })
  query += "}"

  return query.trim()
}

/**
 * Return a Graphql query result flattened
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
