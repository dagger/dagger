import ts from "typescript"

import { UnknownDaggerError } from "../common/errors/UnknownDaggerError.js"

export type SerializedSymbol = {
  name: string
  type: ts.Type
  typeName: string
  doc: string
}

export type SerializedSignature = {
  parameters: SerializedSymbol[]
  returnType: string
  doc: string
}

export function serializeSymbol(
  symbol: ts.Symbol,
  checker: ts.TypeChecker
): SerializedSymbol {
  if (!symbol.valueDeclaration) {
    throw new UnknownDaggerError("could not find symbol value declaration", {})
  }

  const type = checker.getTypeOfSymbolAtLocation(
    symbol,
    symbol.valueDeclaration
  )

  return {
    name: symbol.getName(),
    type: type,
    typeName: checker.typeToString(type),
    doc: ts.displayPartsToString(symbol.getDocumentationComment(checker)),
  }
}

export function serializeSignature(
  signature: ts.Signature,
  checker: ts.TypeChecker
): SerializedSignature {
  return {
    parameters: signature.parameters.map((param) =>
      serializeSymbol(param, checker)
    ),
    returnType: checker.typeToString(signature.getReturnType()),
    doc: ts.displayPartsToString(signature.getDocumentationComment(checker)),
  }
}
