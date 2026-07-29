import assert from "assert"
import { describe, it } from "mocha"
import path from "path"
import { fileURLToPath } from "url"

import { scan } from "../index.js"
import { serializeIntrospection } from "../introspection_json.js"
import { listFiles } from "../utils/files.js"

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const rootDirectory = `${__dirname}/testdata`

// These types mirror the introspection JSON shape (a subset) so the test can
// navigate the emitter output without `any`.
type TypeRef = { kind: string; name?: string; ofType?: TypeRef }
type Field = {
  name: string
  type: TypeRef
  args: { name: string; type: TypeRef; directives?: unknown[] }[]
}
type IntrospectionType = {
  kind: string
  name: string
  fields?: Field[]
  enumValues?: { name: string }[]
}
type Introspection = {
  __schema: { queryType: { name: string }; types: IntrospectionType[] }
}

async function introspect(
  directory: string,
): Promise<Introspection["__schema"]> {
  const files = await listFiles(`${rootDirectory}/${directory}`)
  const module = await scan(files, directory)
  return serializeIntrospection(module as never).__schema
}

function typeByName(
  schema: Introspection["__schema"],
  name: string,
): IntrospectionType | undefined {
  return schema.types.find((t) => t.name === name)
}

function fieldByName(t: IntrospectionType | undefined, name: string) {
  return t?.fields?.find((f) => f.name === name)
}

describe("serializeIntrospection", function () {
  it("emits the main object, its methods and a Node id field", async function () {
    this.timeout(60000)
    const schema = await introspect("helloWorld")

    const obj = typeByName(schema, "HelloWorld")
    assert.ok(obj, "HelloWorld object type is emitted")
    assert.equal(obj?.kind, "OBJECT")

    const method = fieldByName(obj, "helloWorld")
    assert.ok(method, "helloWorld method is emitted as a field")
    // string return -> NON_NULL String
    assert.equal(method?.type.kind, "NON_NULL")
    assert.equal(method?.type.ofType?.name, "String")
    // string arg -> NON_NULL String, named "name"
    assert.equal(method?.args[0]?.name, "name")
    assert.equal(method?.args[0]?.type.kind, "NON_NULL")

    const id = fieldByName(obj, "id")
    assert.ok(id, "synthetic Node id field is emitted")
    assert.equal(id?.type.kind, "NON_NULL")
    assert.equal(id?.type.ofType?.name, "ID")
  })

  it("emits a Query type carrying the module constructor field", async function () {
    this.timeout(60000)
    const schema = await introspect("helloWorld")

    assert.equal(schema.queryType.name, "Query")
    const query = typeByName(schema, "Query")
    const ctor = fieldByName(query, "helloWorld")
    assert.ok(ctor, "Query has the lowerCamel(moduleName) constructor field")
    assert.equal(ctor?.type.kind, "NON_NULL")
    assert.equal(ctor?.type.ofType?.kind, "OBJECT")
    assert.equal(ctor?.type.ofType?.name, "HelloWorld")
  })

  it("namespaces module-local enums and keeps conventional member names", async function () {
    this.timeout(60000)
    const schema = await introspect("enums")

    // enum "Status" in module "Enums" is namespaced to "EnumsStatus".
    const enumType = typeByName(schema, "EnumsStatus")
    assert.ok(enumType, "module enum is namespaced with the module name")
    assert.equal(enumType?.kind, "ENUM")
    const members = (enumType?.enumValues ?? []).map((v) => v.name)
    assert.deepEqual(members, ["ACTIVE", "INACTIVE"])

    // a method taking the enum refers to it by its namespaced name.
    const obj = typeByName(schema, "Enums")
    const setStatus = fieldByName(obj, "setStatus")
    assert.equal(
      setStatus?.args[0]?.type.ofType?.name ?? setStatus?.args[0]?.type.name,
      "EnumsStatus",
    )
  })
})
