/**
 * Metadata of a variable (called symbol in the TypeScript compiler)
 */
export type SymbolMetadata = {
  name: string
  description: string
  typeName: string
}

/**
 * Metadata of a function or method parameter.
 */
export type ParamMetadata = SymbolMetadata & {
  optional: boolean
  defaultValue?: string
  isVariadic: boolean
}

/**
 * Metadata of a function's signature.
 */
export type SignatureMetadata = {
  params: ParamMetadata[]
  returnType: string
}
