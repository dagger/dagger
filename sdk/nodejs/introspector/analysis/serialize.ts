import ts from "typescript"

import { UnknownDaggerError } from "../../common/errors/UnknownDaggerError.js"
import { SignatureMetadata, SymbolMetadata } from "./metadata.js"

export function serializeSignature(
  checker: ts.TypeChecker,
  signature: ts.Signature
): SignatureMetadata {
  return {
    params: signature.parameters.map((param) =>
      serializeSymbol(checker, param)
    ),
    returnType: serializeType(checker, signature.getReturnType()),
  }
}

export function serializeSymbol(
  checker: ts.TypeChecker,
  symbol: ts.Symbol
): SymbolMetadata & { type: ts.Type } {
  if (!symbol.valueDeclaration) {
    throw new UnknownDaggerError("could not find symbol value declaration", {})
  }

  const type = checker.getTypeOfSymbolAtLocation(
    symbol,
    symbol.valueDeclaration
  )

  return {
    name: symbol.getName(),
    doc: ts.displayPartsToString(symbol.getDocumentationComment(checker)),
    typeName: serializeType(checker, type),
    type,
  }
}

export function serializeType(checker: ts.TypeChecker, type: ts.Type): string {
  const strType = checker.typeToString(type)

  // Remove Promise<> wrapper around type if it's a promise.
  if (strType.startsWith("Promise")) {
    return strType.substring("Promise<".length, strType.length - 1)
  }

  return strType
}
