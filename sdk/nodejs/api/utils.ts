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

  return query.replace(/\s+/g, "")
}

/**
 * Return a Graphql query result flattened
 */
let queryResult: any
export function queryFlatten<T>(response: T): T {
  for (const key in response) {
    if (Object.prototype.toString.call(response[key]) === "[object Object]") {
      queryFlatten(response[key])
    } else {
      queryResult = response[key]
    }
  }

  return queryResult
}
