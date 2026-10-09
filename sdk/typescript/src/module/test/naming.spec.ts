import assert from "assert"
import { describe, it } from "mocha"
import path from "path"
import { fileURLToPath } from "url"

import { Executor, interfaceNames } from "../executor.js"
import { scan } from "../introspector/index.js"
import { listFiles } from "../introspector/utils/files.js"
import { resolveSchemaNames, SchemaNames } from "../naming.js"
import type { InterfaceNames, NameCasing } from "../naming.js"

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

// How an engine at v1.0.0 or later formats the names below: acronyms are
// words of their own, which a plain case conversion can't tell.
const ENGINE_NAMES: Record<NameCasing, Record<string, string>> = {
  CAMEL: {
    getUrl: "getURL",
    comUrl: "comURL",
    withHttpClient: "withHTTPClient",
    speak: "speak",
    name: "name",
  },
  SNAKE: {
    Pond: "pond",
    HttpFetcher: "http_fetcher",
    PondSpeaker: "pond_speaker",
  },
  PASCAL: {
    HttpFetcher: "HTTPFetcher",
    PondSpeaker: "PondSpeaker",
    Pond_HttpFetcher: "PondHTTPFetcher",
    Pond_PondSpeaker: "PondPondSpeaker",
  },
}

const calls: { names: string[]; casing: NameCasing }[] = []

async function engineFormatter(
  names: string[],
  casing: NameCasing,
): Promise<string[]> {
  calls.push({ names, casing })
  return names.map((name) => {
    const formatted = ENGINE_NAMES[casing][name]
    assert.ok(formatted, `unexpected ${casing} name ${name}`)
    return formatted
  })
}

const pondInterfaces: InterfaceNames[] = [
  {
    name: "HttpFetcher",
    functions: [
      { name: "getUrl", args: ["comUrl"] },
      { name: "withHttpClient", args: ["name"] },
    ],
  },
  // Already starts with the module's name: not namespaced again.
  { name: "PondSpeaker", functions: [{ name: "speak", args: [] }] },
]

describe("schema names", function () {
  it("resolves interface names with the engine's naming rules", async function () {
    calls.length = 0
    const names = await resolveSchemaNames(
      "Pond",
      pondInterfaces,
      engineFormatter,
    )

    assert.ok(names)
    assert.deepEqual(names.fields, {
      comUrl: "comURL",
      getUrl: "getURL",
      name: "name",
      speak: "speak",
      withHttpClient: "withHTTPClient",
    })
    assert.deepEqual(names.types, {
      HttpFetcher: "PondHTTPFetcher",
      PondSpeaker: "PondSpeaker",
    })

    // One batch per casing.
    assert.deepEqual(
      calls.map((c) => c.casing),
      ["CAMEL", "SNAKE", "PASCAL"],
    )
  })

  it("maps names it resolved, and falls back for the rest", async function () {
    const names = await resolveSchemaNames(
      "Pond",
      pondInterfaces,
      engineFormatter,
    )
    assert.ok(names)

    assert.equal(names.fieldName("getUrl"), "getURL")
    assert.equal(names.fieldName("unknownFn"), "unknownFn")
    assert.equal(
      names.interfaceName("HttpFetcher", "PondHttpFetcher"),
      "PondHTTPFetcher",
    )
    assert.equal(names.interfaceName("Other", "PondOther"), "PondOther")
  })

  it("keeps the legacy names without the engine's", function () {
    const names = new SchemaNames()

    assert.equal(names.fieldName("getUrl"), "getUrl")
    assert.equal(names.fieldName("comUrl"), "comUrl")
    assert.equal(
      names.interfaceName("HttpFetcher", "PondHttpFetcher"),
      "PondHttpFetcher",
    )
  })

  it("doesn't ask the engine for modules without interfaces", async function () {
    calls.length = 0
    const names = await resolveSchemaNames("Pond", [], engineFormatter)

    assert.equal(names, undefined)
    assert.equal(calls.length, 0)
  })

  it("names the interfaces of a scanned module", async function () {
    const files = await listFiles(
      `${__dirname}/../introspector/test/testdata/interface`,
    )
    const module = await scan(files, "interface")
    const ifaces = interfaceNames(module)

    assert.equal(ifaces.length, 1)
    assert.equal(ifaces[0].name, "IFace")
    const count = ifaces[0].functions.find((fn) => fn.name === "count")
    assert.deepEqual(count?.args, ["n"])
    const withSelf = ifaces[0].functions.find((fn) => fn.name === "withSelf")
    assert.deepEqual(withSelf?.args, ["i"])

    // Calls through the interface use the resolved type name, else the
    // name the runtime always used.
    const executor = new Executor([], module)
    assert.equal(executor.interfaceTypeName("IFace"), "InterfaceIFace")
    executor.schemaNames = new SchemaNames({}, { IFace: "InterfaceIFACE" })
    assert.equal(executor.interfaceTypeName("IFace"), "InterfaceIFACE")
  })
})
