import assert from "assert"
import * as path from "path"
import { fileURLToPath } from "url"

import { TypeDefKind } from "../../api/client.gen.js"
import { scan, ScanResult } from "../scanner/scan.js"
import { listFiles } from "../utils/files.js"

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const rootDirectory = `${__dirname}/testdata`

describe("scan static TypeScript", function () {
  it("Should correctly scan a basic class with one method", async function () {
    const files = await listFiles(`${rootDirectory}/helloWorld`)

    const result = scan(files)
    const expected: ScanResult = {
      classes: [
        {
          name: "HelloWorld",
          description: "HelloWorld class",
          fields: [],
          constructor: undefined,
          methods: [
            {
              name: "helloWorld",
              returnType: {
                kind: TypeDefKind.Stringkind,
              },
              description: "",
              args: [
                {
                  name: "name",
                  typeDef: { kind: TypeDefKind.Stringkind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              ],
            },
          ],
        },
      ],
      functions: [],
    }

    assert.deepEqual(result, expected)
  })

  it("Should ignore class that does not have the object decorator", async function () {
    const files = await listFiles(`${rootDirectory}/noDecorators`)

    const result = scan(files)
    const expected: ScanResult = {
      classes: [],
      functions: [],
    }

    assert.deepEqual(result, expected)
  })

  it("Should supports multiple files and classes that returns classes", async function () {
    const files = await listFiles(`${rootDirectory}/multipleObjects`)

    const result = scan(files)
    const expected: ScanResult = {
      classes: [
        {
          name: "Bar",
          description: "Bar class",
          constructor: undefined,
          fields: [],
          methods: [
            {
              name: "exec",
              description: "Execute the command and return its result",
              returnType: { kind: TypeDefKind.Stringkind },
              args: [
                {
                  name: "cmd",
                  typeDef: {
                    kind: TypeDefKind.Listkind,
                    typeDef: {
                      kind: TypeDefKind.Stringkind,
                    },
                  },
                  description: "Command to execute",
                  optional: false,
                  defaultValue: undefined,
                },
              ],
            },
          ],
        },
        {
          name: "Foo",
          description: "Foo class",
          constructor: undefined,
          fields: [],
          methods: [
            {
              name: "bar",
              description: "Return Bar object",
              returnType: {
                kind: TypeDefKind.Objectkind,
                name: "Bar",
              },
              args: [],
            },
          ],
        },
      ],
      functions: [],
    }

    assert.deepEqual(result, expected)
  })

  it("Should not expose private methods from a class", async function () {
    const files = await listFiles(`${rootDirectory}/privateMethod`)

    const result = scan(files)
    const expected: ScanResult = {
      classes: [
        {
          name: "HelloWorld",
          description: "HelloWorld class",
          constructor: undefined,
          fields: [],
          methods: [
            {
              name: "greeting",
              returnType: { kind: TypeDefKind.Stringkind },
              description: "",
              args: [
                {
                  name: "name",
                  typeDef: { kind: TypeDefKind.Stringkind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              ],
            },
            {
              name: "helloWorld",
              returnType: { kind: TypeDefKind.Stringkind },
              description: "",
              args: [
                {
                  name: "name",
                  typeDef: { kind: TypeDefKind.Stringkind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              ],
            },
          ],
        },
      ],
      functions: [],
    }

    assert.deepEqual(result, expected)
  })

  it("should scan classes' properties to keep a state", async function () {
    const files = await listFiles(`${rootDirectory}/state`)

    const result = scan(files)
    const expected: ScanResult = {
      classes: [
        {
          name: "Alpine",
          description: "Alpine module",
          constructor: undefined,
          fields: [
            {
              name: "packages",
              typeDef: {
                kind: TypeDefKind.Listkind,
                typeDef: {
                  kind: TypeDefKind.Stringkind,
                },
              },
              description: "packages to install",
            },
            {
              name: "ctr",
              typeDef: {
                kind: TypeDefKind.Objectkind,
                name: "Container",
              },
              description: "",
            },
          ],
          methods: [
            {
              name: "base",
              returnType: {
                kind: TypeDefKind.Objectkind,
                name: "Alpine",
              },
              description: "Returns a base Alpine container",
              args: [
                {
                  name: "version",
                  typeDef: { kind: TypeDefKind.Stringkind },
                  description: "version to use (default to: 3.16.2)",
                  optional: true,
                  defaultValue: undefined,
                },
              ],
            },
            {
              name: "install",
              returnType: {
                kind: TypeDefKind.Objectkind,
                name: "Alpine",
              },
              description: "",
              args: [
                {
                  name: "pkgs",
                  typeDef: {
                    kind: TypeDefKind.Listkind,
                    typeDef: {
                      kind: TypeDefKind.Stringkind,
                    },
                  },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              ],
            },
            {
              name: "exec",
              returnType: { kind: TypeDefKind.Stringkind },
              description: "",
              args: [
                {
                  name: "cmd",
                  typeDef: {
                    kind: TypeDefKind.Listkind,
                    typeDef: {
                      kind: TypeDefKind.Stringkind,
                    },
                  },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              ],
            },
          ],
        },
      ],
      functions: [],
    }

    assert.deepEqual(result, expected)
  })

  it("Should detect optional parameters of a method", async function () {
    const files = await listFiles(`${rootDirectory}/optionalParameter`)

    const result = scan(files)
    const expected: ScanResult = {
      classes: [
        {
          name: "HelloWorld",
          description: "HelloWorld class",
          fields: [],
          constructor: undefined,
          methods: [
            {
              name: "helloWorld",
              returnType: { kind: TypeDefKind.Stringkind },
              description: "",
              args: [
                {
                  name: "name",
                  typeDef: { kind: TypeDefKind.Stringkind },
                  description: "",
                  optional: true,
                  defaultValue: undefined,
                },
              ],
            },
            {
              name: "isTrue",
              returnType: { kind: TypeDefKind.Booleankind },
              description: "",
              args: [
                {
                  name: "value",
                  typeDef: { kind: TypeDefKind.Booleankind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              ],
            },
            {
              name: "add",
              returnType: { kind: TypeDefKind.Integerkind },
              description: "",
              args: [
                {
                  name: "a",
                  typeDef: { kind: TypeDefKind.Integerkind },
                  description: "",
                  optional: true,
                  defaultValue: "0",
                },
                {
                  name: "b",
                  typeDef: { kind: TypeDefKind.Integerkind },
                  description: "",
                  optional: true,
                  defaultValue: "0",
                },
              ],
            },
          ],
        },
      ],
      functions: [],
    }

    assert.deepEqual(result, expected)
  })

  it("Should correctly handle function with void return", async function () {
    const files = await listFiles(`${rootDirectory}/voidReturn`)

    const result = scan(files)
    const expected: ScanResult = {
      classes: [
        {
          name: "HelloWorld",
          description: "HelloWorld class",
          constructor: undefined,
          fields: [],
          methods: [
            {
              name: "helloWorld",
              returnType: { kind: TypeDefKind.Voidkind },
              description: "",
              args: [
                {
                  name: "name",
                  typeDef: { kind: TypeDefKind.Stringkind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              ],
            },
            {
              name: "asyncHelloWorld",
              returnType: { kind: TypeDefKind.Voidkind },
              description: "",
              args: [
                {
                  name: "name",
                  typeDef: { kind: TypeDefKind.Stringkind },
                  description: "",
                  optional: true,
                  defaultValue: undefined,
                },
              ],
            },
          ],
        },
      ],
      functions: [],
    }

    assert.deepEqual(result, expected)
  })

  it("Should introspect constructor", async function () {
    const files = await listFiles(`${rootDirectory}/constructor`)

    const result = scan(files)
    const expected: ScanResult = {
      classes: [
        {
          name: "HelloWorld",
          description: "HelloWorld class",
          fields: [],
          constructor: {
            args: [
              {
                name: "name",
                typeDef: { kind: TypeDefKind.Stringkind },
                description: "",
                defaultValue: '"world"',
                optional: true,
              },
            ],
          },
          methods: [
            {
              name: "sayHello",
              returnType: {
                kind: TypeDefKind.Stringkind,
              },
              description: "",
              args: [
                {
                  name: "name",
                  typeDef: { kind: TypeDefKind.Stringkind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              ],
            },
          ],
        },
      ],
      functions: [],
    }

    assert.deepEqual(result, expected)
  })
})
