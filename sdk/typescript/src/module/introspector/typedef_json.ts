import { TypeDefKind } from "../../api/client.gen.js"
import { DaggerArgument } from "./dagger_module/argument.js"
import { DaggerEnum, DaggerEnumValue } from "./dagger_module/enum.js"
import { DaggerFunction } from "./dagger_module/function.js"
import { DaggerInterface } from "./dagger_module/interface.js"
import { DaggerInterfaceFunction } from "./dagger_module/interfaceFunction.js"
import { DaggerModule } from "./dagger_module/module.js"
import {
  DaggerObjectBase,
  DaggerObjectPropertyBase,
} from "./dagger_module/objectBase.js"
import { ListTypeDef, ObjectTypeDef, TypeDef } from "./typedef.js"

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

function serializeFunction(fn: DaggerFunction | DaggerInterfaceFunction) {
  // Emit arguments as an ordered array — argument position matters for
  // positional dispatch and Go's `map` iteration loses order.
  const f = fn as DaggerFunction
  return {
    name: f.name,
    alias: f.alias,
    cache: f.cache,
    description: f.description,
    deprecated: f.deprecated,
    isCheck: f.isCheck === true,
    isGenerator: f.isGenerator === true,
    isUp: f.isUp === true,
    returnType: f.returnType ? serializeType(f.returnType) : undefined,
    arguments: Object.values(f.arguments).map(serializeArgument),
  }
}

function serializeArgument(arg: DaggerArgument) {
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

function serializeProperty(prop: DaggerObjectPropertyBase) {
  return {
    name: prop.name,
    alias: prop.alias,
    description: prop.description,
    deprecated: prop.deprecated,
    isExposed: prop.isExposed === true,
    type: prop.type ? serializeType(prop.type) : undefined,
  }
}

function serializeEnum(enum_: DaggerEnum) {
  return {
    name: enum_.name,
    description: enum_.description,
    values: mapValues(enum_.values, (v: DaggerEnumValue) => ({
      name: v.name,
      value: v.value,
      description: v.description,
      deprecated: v.deprecated,
    })),
  }
}

function serializeInterface(iface: DaggerInterface) {
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
        typeDef: serializeType((t as ListTypeDef).typeDef),
      }
    case TypeDefKind.ObjectKind:
    case TypeDefKind.EnumKind:
    case TypeDefKind.InterfaceKind:
    case TypeDefKind.ScalarKind:
      return { kind: t.kind, name: (t as ObjectTypeDef).name }
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
