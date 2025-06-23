import { TypeDefKind } from "../../../api/client.gen.js"
import { IntrospectionError } from "../../../common/errors/index.js"
import { TypeDef } from "../typedef.js"

export function isTypeDefResolved(typeDef: TypeDef<TypeDefKind>): boolean {
  if (typeDef.kind !== TypeDefKind.List) {
    return true
  }

  const arrayTypeDef = typeDef as TypeDef<TypeDefKind.List>

  if (arrayTypeDef.typeDef === undefined) {
    return false
  }

  if (arrayTypeDef.typeDef.kind === TypeDefKind.List) {
    return isTypeDefResolved(arrayTypeDef.typeDef)
  }

  return true
}

export function resolveTypeDef(
  typeDef: TypeDef<TypeDefKind> | undefined,
  reference: TypeDef<TypeDefKind>,
): TypeDef<TypeDefKind> {
  if (typeDef === undefined) {
    return reference
  }

  if (typeDef.kind === TypeDefKind.List) {
    const listTypeDef = typeDef as TypeDef<TypeDefKind.List>

    listTypeDef.typeDef = resolveTypeDef(listTypeDef.typeDef, reference)
    return listTypeDef
  }

  throw new IntrospectionError(
    `type ${JSON.stringify(typeDef)} has already been resolved, it should not be overwritten ; reference: ${JSON.stringify(reference)}`,
  )
}
