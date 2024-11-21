import { TypeDefKind } from "../../api/client.gen.js"

/**
 * Base type of argument, field or return type.
 */
export type BaseTypeDef = {
  kind: TypeDefKind
}

/**
 * Extends the base type def if it's an object to add its name.
 */
export type ObjectTypeDef = BaseTypeDef & {
  kind: TypeDefKind.ObjectKind
  name: string
}

/**
 * Extends the base type def if it's an enum to add its name.
 */
export type EnumTypeDef = BaseTypeDef & {
  kind: TypeDefKind.EnumKind
  name: string
}

/**
 * Extends the base typedef if it's a scalar to add its name and real type.
 */
export type ScalarTypeDef = BaseTypeDef & {
  kind: TypeDefKind.ScalarKind
  name: string
}

/**
 * Extends the base if it's a list to add its subtype.
 */
export type ListTypeDef = BaseTypeDef & {
  kind: TypeDefKind.ListKind
  typeDef: TypeDef<TypeDefKind>
}

/**
 * A generic TypeDef that will dynamically add necessary properties
 * depending on its type.
 *
 * If it's a type of kind scalar, it transforms the BaseTypeDef into a ScalarTypeDef.
 * If it's type of kind object, it transforms the BaseTypeDef into an ObjectTypeDef.
 * If it's a type of kind list, it transforms the BaseTypeDef into a ListTypeDef.
 */
export type TypeDef<T extends BaseTypeDef["kind"]> =
  T extends TypeDefKind.ScalarKind
    ? ScalarTypeDef
    : T extends TypeDefKind.ObjectKind
      ? ObjectTypeDef
      : T extends TypeDefKind.ListKind
        ? ListTypeDef
        : T extends TypeDefKind.EnumKind
          ? EnumTypeDef
          : BaseTypeDef
