import { TypeDefKind } from "../../api/client.gen.js"
import { DaggerModule } from "./dagger_module/module.js"
import { DaggerObjectBase } from "./dagger_module/objectBase.js"
import { TypeDef } from "./typedef.js"

/**
 * Serialize a parsed DaggerModule into a stable JSON shape that downstream
 * codegen (in Go, see `cmd/codegen/generator/typescript/entrypoint.go`) can
 * read to render the static dispatch entrypoint.
 *
 * This is intentionally a separate path from the existing `toJSON` methods
 * so we can include fields the emitter needs (`kind`, `isExported`,
 * `location`, `cache`, `isCheck`, ...) without disturbing other consumers.
 */
export function serializeModule(module: DaggerModule): unknown {
  return {
    name: module.name,
    description: module.description,
    objects: mapValues(module.objects, serializeObject),
    enums: mapValues(module.enums, serializeEnum),
    interfaces: mapValues(module.interfaces, serializeInterface),
  }
}

function serializeObject(obj: DaggerObjectBase) {
  const isExported = (obj as DaggerObjectBase & { isExported?: boolean })
    .isExported
  const ctor = obj._constructor
  return {
    name: obj.name,
    kind: obj.kind(),
    isExported: isExported !== false,
    description: obj.description,
    deprecated: obj.deprecated,
    location: obj.getLocation(),
    constructor: ctor
      ? {
          name: ctor.name,
          arguments: Object.values(ctor.arguments).map(serializeArgument),
        }
      : undefined,
    methods: mapValues(obj.methods, serializeFunction),
    properties: mapValues(obj.properties, serializeProperty),
  }
}

function serializeFunction(fn: any) {
  // Emit arguments as an ordered array — argument position matters for
  // positional dispatch and Go's `map` iteration loses order.
  return {
    name: fn.name,
    alias: fn.alias,
    cache: fn.cache,
    description: fn.description,
    deprecated: fn.deprecated,
    isCheck: fn.isCheck === true,
    isGenerator: fn.isGenerator === true,
    isUp: fn.isUp === true,
    returnType: fn.returnType ? serializeType(fn.returnType) : undefined,
    arguments: Object.values(fn.arguments).map(serializeArgument),
  }
}

function serializeArgument(arg: any) {
  return {
    name: arg.name,
    description: arg.description,
    deprecated: arg.deprecated,
    type: arg.type ? serializeType(arg.type) : undefined,
    isVariadic: arg.isVariadic === true,
    isNullable: arg.isNullable === true,
    isOptional: arg.isOptional === true,
    defaultValue: arg.defaultValue,
    defaultPath: arg.defaultPath,
    defaultAddress: arg.defaultAddress,
    ignore: arg.ignore,
  }
}

function serializeProperty(prop: any) {
  return {
    name: prop.name,
    alias: prop.alias,
    description: prop.description,
    deprecated: prop.deprecated,
    isExposed: prop.isExposed === true,
    type: prop.type ? serializeType(prop.type) : undefined,
  }
}

function serializeEnum(enum_: any) {
  return {
    name: enum_.name,
    description: enum_.description,
    values: mapValues(enum_.values, (v: any) => ({
      name: v.name,
      value: v.value,
      description: v.description,
      deprecated: v.deprecated,
    })),
  }
}

function serializeInterface(iface: any) {
  return {
    name: iface.name,
    description: iface.description,
    functions: mapValues(iface.functions, serializeFunction),
  }
}

function serializeType(t: TypeDef<TypeDefKind>): unknown {
  switch (t.kind) {
    case TypeDefKind.ListKind:
      return {
        kind: t.kind,
        typeDef: serializeType((t as any).typeDef),
      }
    case TypeDefKind.ObjectKind:
    case TypeDefKind.EnumKind:
    case TypeDefKind.InterfaceKind:
    case TypeDefKind.ScalarKind:
      return { kind: t.kind, name: (t as any).name }
    default:
      return { kind: t.kind }
  }
}

function mapValues<T, U>(
  obj: Record<string, T>,
  fn: (value: T) => U,
): Record<string, U> {
  const out: Record<string, U> = {}
  for (const [k, v] of Object.entries(obj)) {
    out[k] = fn(v)
  }
  return out
}
