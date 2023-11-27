export type SymbolMetadata = {
  name: string
  doc: string
  typeName: string
}

export type ParamMetadata = SymbolMetadata

export type SignatureMetadata = {
  params: ParamMetadata[]
  returnType: string
}

export type FunctionMetadata = Omit<SymbolMetadata, "typeName"> & {
  params: ParamMetadata[]
  returnType: string
}

export type ClassMetadata = Omit<SymbolMetadata, "typeName"> & {
  methods: FunctionMetadata[]
}

export type Metadata = {
  classes: ClassMetadata[]
  functions: FunctionMetadata[]
}
