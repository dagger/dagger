export type Arg = {
  name: string
  type: string
  doc: string
}

export type Return = string

/**
 * EntrypointMetadata defines the metadata of a function that acts
 * as an entrypoint.
 * It contains all important properties required to generate a GraphQL schema
 * from this.
 */
export type EntrypointMetadata = {
  name: string
  doc: string
  args: Arg[]
  return: Return
}
