import { TypeDefKind } from "../../../api/client.gen.js"
import { TypeDef } from "../typedef.js"

export type References = { [name: string]: TypeDef<TypeDefKind> }

export type ReferencableType =
  | TypeDef<TypeDefKind.Object>
  | TypeDef<TypeDefKind.Enum>
  | TypeDef<TypeDefKind.Float>
  | TypeDef<TypeDefKind.Scalar>
  | TypeDef<TypeDefKind.Interface>

export function isKindArray(
  type: TypeDef<TypeDefKind>,
): type is TypeDef<TypeDefKind.List> {
  return type.kind === TypeDefKind.List
}

export function isKindObject(
  type: TypeDef<TypeDefKind>,
): type is TypeDef<TypeDefKind.Object> {
  return type.kind === TypeDefKind.Object
}

export function isKindEnum(
  type: TypeDef<TypeDefKind>,
): type is TypeDef<TypeDefKind.Enum> {
  return type.kind === TypeDefKind.Enum
}

export function isKindScalar(
  type: TypeDef<TypeDefKind>,
): type is TypeDef<TypeDefKind.Scalar> {
  return type.kind === TypeDefKind.Scalar
}

export function isReferencableTypeDef(type: TypeDef<TypeDefKind>): boolean {
  switch (type.kind) {
    case TypeDefKind.Object:
      return true
    case TypeDefKind.Enum:
      return true
    case TypeDefKind.Scalar:
      return true
    case TypeDefKind.List:
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
