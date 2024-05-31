import ts from "typescript"

import { TypeDefKind } from "../../../api/client.gen.js"
import { TypeDef } from "../typeDefs.js"
import { isEnumDecorated } from "./enum.js"

/**
 * Convert a type into a Dagger Typedef using dynamic typing.
 */
export function typeToTypedef(
  checker: ts.TypeChecker,
  type: ts.Type,
): TypeDef<TypeDefKind> {
  const symbolName = type.getSymbol()?.name
  const symbolDeclaration = type.getSymbol()?.valueDeclaration

  if (symbolName === "Promise") {
    const typeArgs = checker.getTypeArguments(type as ts.TypeReference)
    if (typeArgs.length > 0) {
      return typeToTypedef(checker, typeArgs[0])
    }
  }

  if (symbolName === "Array") {
    const typeArgs = checker.getTypeArguments(type as ts.TypeReference)
    if (typeArgs.length === 0) {
      throw new Error("Generic array not supported")
    }
    return {
      kind: TypeDefKind.ListKind,
      typeDef: typeToTypedef(checker, typeArgs[0]),
    }
  }

  if (
    symbolName &&
    type.isClassOrInterface() &&
    symbolDeclaration &&
    ts.isClassDeclaration(symbolDeclaration)
  ) {
    if (isEnumDecorated(symbolDeclaration)) {
      return {
        kind: TypeDefKind.EnumKind,
        name: symbolName,
      }
    }

    return {
      kind: TypeDefKind.ObjectKind,
      name: symbolName,
    }
  }

  const strType = checker.typeToString(type)

  switch (strType) {
    case "string":
      return { kind: TypeDefKind.StringKind }
    case "number":
      return { kind: TypeDefKind.IntegerKind }
    case "boolean":
      return { kind: TypeDefKind.BooleanKind }
    case "void":
      return { kind: TypeDefKind.VoidKind }
    default:
      // If it's a union, then it's a scalar type
      if (type.isUnionOrIntersection()) {
        return {
          kind: TypeDefKind.ScalarKind,
          name: strType,
        }
      }

      throw new Error(`Unsupported type ${strType}`)
  }
}
