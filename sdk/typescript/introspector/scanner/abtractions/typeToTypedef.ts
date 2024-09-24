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
  const symbol = type.getSymbol()
  const symbolName = symbol?.name
  const symbolDeclaration = symbol?.valueDeclaration

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
    // Intercept primitive types and throw error in this case
    case "String":
      throw new Error(
        "Use of primitive String type detected, did you mean string?",
      )
    case "Number":
      throw new Error(
        "Use of primitive Number type detected, did you mean number?",
      )
    case "Boolean":
      throw new Error(
        "Use of primitive Boolean type detected, did you mean boolean?",
      )
    default:
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

      if (
        symbol?.getFlags() !== undefined &&
        (symbol.getFlags() & ts.SymbolFlags.Enum) !== 0
      ) {
        return {
          kind: TypeDefKind.EnumKind,
          name: strType,
        }
      }

      // If it's a union, then it's a scalar type
      if (type.isUnionOrIntersection()) {
        return {
          kind: TypeDefKind.ScalarKind,
          name: strType,
        }
      }

      // If we cannot resolve the symbol, we check for the alias symbol.
      // This should mostly lead to a failure since external types are not supported by
      // dagger yet.
      if (type.aliasSymbol && type.aliasSymbol.flags & ts.TypeFlags.Object) {
        return {
          kind: TypeDefKind.ObjectKind,
          name: type.aliasSymbol.escapedName.toString(),
        }
      }

      throw new Error(`Unsupported type ${strType}`)
  }
}
