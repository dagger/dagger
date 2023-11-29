/**
 * Metadata of a variable (called symbol in the Typescript compiler)
 */
export type SymbolMetadata = {
  name: string
  doc: string
  typeName: string
}

/**
 * Metadata of a function or method parameter.
 */
export type ParamMetadata = SymbolMetadata & {
  optional: boolean
  defaultValue?: string
}

/**
 * Metadata of a class' property.
 */
export type PropertyMetadata = SymbolMetadata

/**
 * Metadata of a function's signature.
 */
export type SignatureMetadata = {
  params: ParamMetadata[]
  returnType: string
}

/**
 * Metadata of a function
 *
 * We exclude the typename since it's not relevant for a function.
 */
export type FunctionMetadata = Omit<SymbolMetadata, "typeName"> & {
  params: ParamMetadata[]
  returnType: string
}

/*
 * Metadata of a class.
 *
 * We exclude the typename since it's not relevant for class.
 */
export type ClassMetadata = Omit<SymbolMetadata, "typeName"> & {
  methods: FunctionMetadata[]
  properties: PropertyMetadata[]
}

/**
 * Metadata of the functions and classes of a Typescript file
 */
export type Metadata = {
  classes: ClassMetadata[]
  functions: FunctionMetadata[]
}
