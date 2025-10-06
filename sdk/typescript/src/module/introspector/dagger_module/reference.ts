import { TypeDefKind } from "../../../api/client.gen.js"
import { TypeDef } from "../typedef.js"

export type References = { [name: string]: TypeDef<TypeDefKind> }

export type ReferencableType =
  | TypeDef<TypeDefKind.ObjectKind>
  | TypeDef<TypeDefKind.EnumKind>
  | TypeDef<TypeDefKind.FloatKind>
  | TypeDef<TypeDefKind.ScalarKind>
  | TypeDef<TypeDefKind.InterfaceKind>

export function isKindArray(
  type: TypeDef<TypeDefKind>,
): type is TypeDef<TypeDefKind.ListKind> {
  return type.kind === TypeDefKind.ListKind
}

export function isKindObject(
  type: TypeDef<TypeDefKind>,
): type is TypeDef<TypeDefKind.ObjectKind> {
  return type.kind === TypeDefKind.ObjectKind
}

export function isKindEnum(
  type: TypeDef<TypeDefKind>,
): type is TypeDef<TypeDefKind.EnumKind> {
  return type.kind === TypeDefKind.EnumKind
}

export function isKindScalar(
  type: TypeDef<TypeDefKind>,
): type is TypeDef<TypeDefKind.ScalarKind> {
  return type.kind === TypeDefKind.ScalarKind
}

export function isReferencableTypeDef(type: TypeDef<TypeDefKind>): boolean {
  switch (type.kind) {
    case TypeDefKind.ObjectKind:
      return true
    case TypeDefKind.EnumKind:
      return true
    case TypeDefKind.ScalarKind:
      return true
    case TypeDefKind.ListKind:
      return isReferencableTypeDef(getTypeDefArrayBaseType(type))
    default:
      return false
  }
}

export function getTypeDefArrayBaseType(
  type: TypeDef<TypeDefKind>,
): TypeDef<TypeDefKind> {
  if (isKindArray(type)) {
    return getTypeDefArrayBaseType(type.typeDef)
  }

  return type
}
