import { TypeDefKind } from "../../api/client.gen.js"
import { DaggerArgument } from "./dagger_module/argument.js"
import { DaggerConstructor } from "./dagger_module/constructor.js"
import { DaggerEnumBase } from "./dagger_module/enumBase.js"
import { DaggerFunction } from "./dagger_module/function.js"
import { DaggerInterface } from "./dagger_module/interface.js"
import { DaggerInterfaceFunction } from "./dagger_module/interfaceFunction.js"
import { DaggerModule } from "./dagger_module/module.js"
import {
  DaggerObjectBase,
  DaggerObjectPropertyBase,
} from "./dagger_module/objectBase.js"
import {
  EnumTypeDef,
  InterfaceTypeDef,
  ListTypeDef,
  ObjectTypeDef,
  ScalarTypeDef,
  TypeDef,
} from "./typedef.js"

// This file is the TypeScript counterpart of the Go SDK's
// `cmd/codegen/generator/go/templates/introspect_emit.go`. It walks a parsed
// `DaggerModule` and emits an introspection-shaped JSON of the module's own
// types, in the exact shape the engine schema tool (`dag.Schema().Merge`) and
// the client-bindings generator consume. It mirrors `Register`
// (`../entrypoint/register.ts`) — the source of truth for how each TypeScript
// construct becomes an engine TypeDef — so the merged schema matches what the
// engine builds from the same source, enabling self calls in the generated
// bindings without a runtime codegen pass.

type TypeRef = {
  kind: string
  name?: string
  ofType?: TypeRef
}

type DirectiveArg = { name: string; value?: string }
type Directive = { name: string; args: DirectiveArg[] }

type InputValue = {
  name: string
  description: string
  defaultValue?: string
  type: TypeRef
  directives?: Directive[]
  isDeprecated?: boolean
  deprecationReason?: string
}

type Field = {
  name: string
  description: string
  type: TypeRef
  args: InputValue[]
  isDeprecated?: boolean
  deprecationReason?: string
  directives?: Directive[]
}

type EnumValue = {
  name: string
  description: string
  isDeprecated?: boolean
  deprecationReason?: string
}

type IntrospectionType = {
  kind: string
  name: string
  description?: string
  fields?: Field[]
  enumValues?: EnumValue[]
  interfaces: IntrospectionType[]
}

type IntrospectionResponse = {
  __schema: {
    queryType: { name: string }
    types: IntrospectionType[]
  }
}

const TypeKind = {
  Scalar: "SCALAR",
  Object: "OBJECT",
  Interface: "INTERFACE",
  Enum: "ENUM",
  List: "LIST",
  NonNull: "NON_NULL",
} as const

const Scalar = {
  Int: "Int",
  Float: "Float",
  String: "String",
  Boolean: "Boolean",
  Void: "Void",
} as const

export type SerializeIntrospectionOptions = {
  // When true, emit a per-type `<T>ID` scalar for every object and interface,
  // matching the Go SDK's `legacyGoSDKCompat` path: legacy (pre-cutover) schema
  // views render module `id` fields as a `<T>ID` alias, so those scalar types
  // must exist in the merged schema or the generated bindings won't type-check.
  legacySharedIDTypes?: boolean
}

/**
 * Serialize a parsed DaggerModule into an introspection JSON of the module's
 * own types. The output is the `moduleTypes` argument to `Schema.Merge`.
 */
export function serializeIntrospection(
  module: DaggerModule,
  opts: SerializeIntrospectionOptions = {},
): IntrospectionResponse {
  const moduleName = module.name
  const localTypeNames = collectLocalTypeNames(module)

  const types: IntrospectionType[] = []

  for (const object of Object.values(module.objects)) {
    types.push(introspectObject(object, moduleName, localTypeNames))
  }
  for (const iface of Object.values(module.interfaces)) {
    types.push(introspectInterface(iface, moduleName, localTypeNames))
  }
  for (const enum_ of Object.values(module.enums)) {
    types.push(introspectEnum(enum_, moduleName))
  }

  if (opts.legacySharedIDTypes) {
    for (const t of [...types]) {
      if (t.kind === TypeKind.Object || t.kind === TypeKind.Interface) {
        types.push({
          kind: TypeKind.Scalar,
          name: `${t.name}ID`,
          description: "A unique identifier for an object.",
          interfaces: [],
        })
      }
    }
  }

  types.push(introspectQuery(module, moduleName, localTypeNames))

  return {
    __schema: {
      queryType: { name: "Query" },
      types,
    },
  }
}

// collectLocalTypeNames returns the raw (un-namespaced) names of every type the
// module declares. Used to decide whether a referenced type name is
// module-local (and must be namespaced like the engine does) or a core /
// dependency type (already carrying its final schema name).
function collectLocalTypeNames(module: DaggerModule): Set<string> {
  const names = new Set<string>()
  for (const o of Object.values(module.objects)) names.add(o.name)
  for (const i of Object.values(module.interfaces)) names.add(i.name)
  for (const e of Object.values(module.enums)) names.add(e.name)
  return names
}

function introspectObject(
  object: DaggerObjectBase,
  moduleName: string,
  local: Set<string>,
): IntrospectionType {
  const name = introspectTypeName(object.name, moduleName)
  const fields: Field[] = []

  for (const method of Object.values(object.methods)) {
    if (toLowerCamel(method.name) === "id") {
      continue
    }
    fields.push(introspectMethod(method, moduleName, local))
  }

  for (const field of Object.values(object.properties)) {
    if (!field.isExposed) {
      continue
    }
    if (toLowerCamel(field.alias ?? field.name) === "id") {
      continue
    }
    fields.push(introspectProperty(field, moduleName, local))
  }

  fields.push(nodeIDField(name))

  return {
    kind: TypeKind.Object,
    name,
    description: trim(object.description),
    interfaces: [],
    fields,
  }
}

function introspectInterface(
  iface: DaggerInterface,
  moduleName: string,
  local: Set<string>,
): IntrospectionType {
  const name = introspectTypeName(iface.name, moduleName)
  const fields: Field[] = []
  for (const fn of Object.values(iface.functions)) {
    if (toLowerCamel(fn.name) === "id") {
      continue
    }
    fields.push(introspectMethod(fn, moduleName, local))
  }
  fields.push(nodeIDField(name))
  return {
    kind: TypeKind.Interface,
    name,
    description: trim(iface.description),
    interfaces: [],
    fields,
  }
}

function introspectEnum(
  enum_: DaggerEnumBase,
  moduleName: string,
): IntrospectionType {
  const values: EnumValue[] = []
  for (const value of Object.values(enum_.values)) {
    const ev: EnumValue = {
      name: gqlEnumMemberName(value.name),
      description: trim(value.description),
    }
    if (value.deprecated !== undefined) {
      ev.isDeprecated = true
      ev.deprecationReason = trim(value.deprecated)
    }
    values.push(ev)
  }
  return {
    kind: TypeKind.Enum,
    name: introspectTypeName(enum_.name, moduleName),
    description: trim(enum_.description),
    interfaces: [],
    enumValues: values,
  }
}

// nodeIDField mirrors the `id` field the engine adds to every module object and
// interface (Node). The bindings key their ID marshalling on it, so without it a
// module type could not be passed as an object argument.
function nodeIDField(typeName: string): Field {
  return {
    name: "id",
    description: `A unique identifier for this ${typeName}.`,
    type: idRef(),
    args: [],
  }
}

function introspectMethod(
  fn: DaggerFunction | DaggerInterfaceFunction,
  moduleName: string,
  local: Set<string>,
): Field {
  const returnType = (fn as DaggerFunction).returnType
  const field: Field = {
    name: toLowerCamel((fn as DaggerFunction).alias ?? fn.name),
    description: trim(fn.description),
    type: returnType
      ? introspectTypeRef(returnType, moduleName, local)
      : voidRef(),
    args: introspectArgs(fn.arguments, moduleName, local),
  }
  const deprecated = (fn as DaggerFunction).deprecated
  if (deprecated !== undefined) {
    field.isDeprecated = true
    field.deprecationReason = trim(deprecated)
  }
  return field
}

function introspectProperty(
  field: DaggerObjectPropertyBase,
  moduleName: string,
  local: Set<string>,
): Field {
  const f: Field = {
    name: toLowerCamel(field.alias ?? field.name),
    description: trim(field.description),
    type: field.type
      ? introspectTypeRef(field.type, moduleName, local)
      : voidRef(),
    args: [],
  }
  if (field.deprecated !== undefined) {
    f.isDeprecated = true
    f.deprecationReason = trim(field.deprecated)
  }
  return f
}

function introspectArgs(
  args: Record<string, DaggerArgument>,
  moduleName: string,
  local: Set<string>,
): InputValue[] {
  const out: InputValue[] = []
  for (const arg of Object.values(args)) {
    out.push(introspectArg(arg, moduleName, local))
  }
  return out
}

// introspectArg mirrors Register.addArg: object/interface args are passed by ID
// with an `@expectedType` directive, optionality strips the NON_NULL wrapper and
// resolvable primitive defaults are carried as a JSON-encoded defaultValue.
function introspectArg(
  arg: DaggerArgument,
  moduleName: string,
  local: Set<string>,
): InputValue {
  const { ref, expectedType } = introspectArgTypeRef(
    arg.type,
    moduleName,
    local,
  )

  let type = ref
  if (arg.isOptional && type.kind === TypeKind.NonNull && type.ofType) {
    type = type.ofType
  }

  const iv: InputValue = {
    name: toLowerCamel(arg.name),
    description: trim(arg.description),
    type,
  }

  if (expectedType) {
    iv.directives = [
      {
        name: "expectedType",
        args: [{ name: "name", value: JSON.stringify(expectedType) }],
      },
    ]
  }

  const defaultValue = resolveDefaultValue(arg)
  if (defaultValue !== undefined) {
    iv.defaultValue = JSON.stringify(defaultValue)
  }

  if (arg.deprecated !== undefined) {
    iv.isDeprecated = true
    iv.deprecationReason = trim(arg.deprecated)
  }

  return iv
}

// resolveDefaultValue mirrors Register.getDefaultValueFromArg: only primitive
// (and enum) defaults are carried in the schema; non-primitive defaults are
// resolved by the runtime instead, so they are omitted here.
function resolveDefaultValue(arg: DaggerArgument): unknown | undefined {
  if (arg.defaultValue === undefined) {
    return undefined
  }
  if (!isPrimitiveKind(arg.type?.kind)) {
    return undefined
  }
  // Enum defaults would need to resolve the raw value back to the member name
  // (as the engine registers them); omit rather than risk emitting an invalid
  // wire value. Enum-typed args with a default are rare and the arg stays
  // usable (just not defaulted in the generated binding).
  if (arg.type?.kind === TypeDefKind.EnumKind) {
    return undefined
  }
  return arg.defaultValue
}

function introspectArgTypeRef(
  spec: TypeDef<TypeDefKind> | undefined,
  moduleName: string,
  local: Set<string>,
): { ref: TypeRef; expectedType: string } {
  if (!spec) {
    return { ref: voidRef(), expectedType: "" }
  }
  switch (spec.kind) {
    case TypeDefKind.ListKind: {
      const { ref, expectedType } = introspectArgTypeRef(
        (spec as ListTypeDef).typeDef,
        moduleName,
        local,
      )
      return {
        ref: nonNull({ kind: TypeKind.List, ofType: ref }),
        expectedType,
      }
    }
    case TypeDefKind.ObjectKind:
      return {
        ref: idRef(),
        expectedType: refTypeName(
          (spec as ObjectTypeDef).name,
          moduleName,
          local,
        ),
      }
    case TypeDefKind.InterfaceKind:
      return {
        ref: idRef(),
        expectedType: refTypeName(
          (spec as InterfaceTypeDef).name,
          moduleName,
          local,
        ),
      }
    default:
      return {
        ref: introspectTypeRef(spec, moduleName, local),
        expectedType: "",
      }
  }
}

// introspectTypeRef converts a scanner TypeDef into its introspection TypeRef.
// Nullability mirrors Register.addTypeDef: everything is NON_NULL except Void
// (arg-level optionality is applied separately by introspectArg).
function introspectTypeRef(
  spec: TypeDef<TypeDefKind>,
  moduleName: string,
  local: Set<string>,
): TypeRef {
  switch (spec.kind) {
    case TypeDefKind.StringKind:
      return nonNull(scalarRef(Scalar.String))
    case TypeDefKind.IntegerKind:
      return nonNull(scalarRef(Scalar.Int))
    case TypeDefKind.BooleanKind:
      return nonNull(scalarRef(Scalar.Boolean))
    case TypeDefKind.FloatKind:
      return nonNull(scalarRef(Scalar.Float))
    case TypeDefKind.VoidKind:
      return voidRef()
    case TypeDefKind.ScalarKind:
      return nonNull(scalarRef((spec as ScalarTypeDef).name))
    case TypeDefKind.ListKind:
      return nonNull({
        kind: TypeKind.List,
        ofType: introspectTypeRef(
          (spec as ListTypeDef).typeDef,
          moduleName,
          local,
        ),
      })
    case TypeDefKind.ObjectKind:
      return nonNull({
        kind: TypeKind.Object,
        name: refTypeName((spec as ObjectTypeDef).name, moduleName, local),
      })
    case TypeDefKind.InterfaceKind:
      return nonNull({
        kind: TypeKind.Interface,
        name: refTypeName((spec as InterfaceTypeDef).name, moduleName, local),
      })
    case TypeDefKind.EnumKind:
      return nonNull({
        kind: TypeKind.Enum,
        name: refTypeName((spec as EnumTypeDef).name, moduleName, local),
      })
    default:
      return voidRef()
  }
}

// introspectQuery builds the Query type carrying the module's constructor field,
// used by Schema.Merge to install the module's entrypoint on the schema's Query.
function introspectQuery(
  module: DaggerModule,
  moduleName: string,
  local: Set<string>,
): IntrospectionType {
  const query: IntrospectionType = {
    kind: TypeKind.Object,
    name: "Query",
    interfaces: [],
    fields: [],
  }

  const mainObject = findMainObject(module, moduleName)
  if (mainObject) {
    const field: Field = {
      name: toLowerCamel(moduleName),
      description: "",
      type: nonNull({
        kind: TypeKind.Object,
        name: introspectTypeName(mainObject.name, moduleName),
      }),
      args: mainObject._constructor
        ? introspectConstructorArgs(mainObject._constructor, moduleName, local)
        : [],
    }
    query.fields!.push(field)
  }

  return query
}

function introspectConstructorArgs(
  ctor: DaggerConstructor,
  moduleName: string,
  local: Set<string>,
): InputValue[] {
  return introspectArgs(ctor.arguments, moduleName, local)
}

// findMainObject returns the module's main object — the one whose PascalCase
// name matches the module name — which carries the constructor.
function findMainObject(
  module: DaggerModule,
  moduleName: string,
): DaggerObjectBase | undefined {
  const target = toPascal(moduleName)
  for (const object of Object.values(module.objects)) {
    if (toPascal(object.name) === target) {
      return object
    }
  }
  return undefined
}

function scalarRef(name: string): TypeRef {
  return { kind: TypeKind.Scalar, name }
}

function nonNull(ofType: TypeRef): TypeRef {
  return { kind: TypeKind.NonNull, ofType }
}

function idRef(): TypeRef {
  return nonNull(scalarRef("ID"))
}

// voidRef is emitted nullable (no NON_NULL wrapper) because the engine always
// marks a Void return optional; a NON_NULL Void would diverge from the
// engine-built schema.
function voidRef(): TypeRef {
  return scalarRef(Scalar.Void)
}

function isPrimitiveKind(kind: TypeDefKind | undefined): boolean {
  return (
    kind === TypeDefKind.BooleanKind ||
    kind === TypeDefKind.IntegerKind ||
    kind === TypeDefKind.StringKind ||
    kind === TypeDefKind.FloatKind ||
    kind === TypeDefKind.EnumKind
  )
}

// refTypeName namespaces a referenced type name only when it is module-local,
// exactly as the engine namespaces module objects/interfaces/enums; core and
// dependency types already carry their final schema name.
function refTypeName(
  name: string,
  moduleName: string,
  local: Set<string>,
): string {
  return local.has(name) ? introspectTypeName(name, moduleName) : toPascal(name)
}

// introspectTypeName maps a module-local type name to the name the engine
// installs it under, mirroring introspect_emit.go's namespaceTypeName.
function introspectTypeName(name: string, moduleName: string): string {
  return namespaceTypeName(name, moduleName)
}

// namespaceTypeName mirrors the engine's namespaceObject (core/gqlformat.go)
// for the case where a type's final and original names are equal — always true
// for the module's own view of itself. Keep in sync.
function namespaceTypeName(typeName: string, moduleName: string): string {
  const camel = toPascal(typeName)
  const modName = toPascal(moduleName)
  if (camel.startsWith(modName)) {
    const rest = camel.slice(modName.length)
    if (rest.length === 0) {
      // The main module object keeps the module's name.
      return modName
    }
    // Only treat the prefix as a namespace on a word boundary: type "Postman"
    // in module "post" must become "PostPostman", while "PostMan" is already
    // namespaced.
    if (rest[0] >= "A" && rest[0] <= "Z") {
      return camel
    }
  }
  return toPascal(`${modName}_${typeName}`)
}

// gqlEnumMemberName mirrors the engine's enum member naming
// (gqlEnumMemberName in core/gqlformat.go): already-conventional GraphQL member
// names such as HTTP2 are kept as-is, everything else becomes SCREAMING_SNAKE.
function gqlEnumMemberName(name: string): string {
  if (isConventionalGraphQLEnumMemberName(name)) {
    return name
  }
  return toScreamingSnake(name)
}

function isConventionalGraphQLEnumMemberName(name: string): boolean {
  if (name === "" || name.startsWith("__")) {
    return false
  }
  for (let i = 0; i < name.length; i++) {
    const c = name[i]
    if (c >= "A" && c <= "Z") {
      continue
    }
    if (i > 0 && ((c >= "0" && c <= "9") || c === "_")) {
      continue
    }
    return false
  }
  return true
}

function toScreamingSnake(input: string): string {
  return input
    .replace(/([a-z0-9])([A-Z])/g, "$1_$2")
    .replace(/([A-Z]+)([A-Z][a-z])/g, "$1_$2")
    .replace(/([a-zA-Z])([0-9])/g, "$1_$2")
    .replace(/[^a-zA-Z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .toUpperCase()
}

// toPascal / toLowerCamel are faithful ports of iancoleman/strcase
// ToCamel / ToLowerCamel — the exact functions the engine uses for
// gqlObjectName / gqlFieldName / gqlArgName. A different casing (e.g. the
// scanner's convertToPascalCase, which splits on every case boundary) would
// emit type/field names the engine never serves, breaking self calls.
function toPascal(name: string): string {
  return toCamelInitCase(name, true)
}

function toLowerCamel(name: string): string {
  return toCamelInitCase(name, false)
}

function toCamelInitCase(s: string, initCase: boolean): string {
  s = s.trim()
  if (s === "") {
    return s
  }
  let out = ""
  let capNext = initCase
  for (let i = 0; i < s.length; i++) {
    let v = s[i]
    const isCap = v >= "A" && v <= "Z"
    const isLow = v >= "a" && v <= "z"
    if (capNext) {
      if (isLow) {
        v = v.toUpperCase()
      }
    } else if (i === 0) {
      if (isCap) {
        v = v.toLowerCase()
      }
    }
    if (isCap || isLow) {
      out += v
      capNext = false
    } else if (v >= "0" && v <= "9") {
      out += v
      capNext = true
    } else {
      capNext = v === "_" || v === " " || v === "-" || v === "."
    }
  }
  return out
}

function trim(s: string | undefined): string {
  return (s ?? "").trim()
}
