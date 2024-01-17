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
                kind: TypeDefKind.StringKind,
              },
              description: "",
              args: [
                {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
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
              returnType: { kind: TypeDefKind.StringKind },
              args: [
                {
                  name: "cmd",
                  typeDef: {
                    kind: TypeDefKind.ListKind,
                    typeDef: {
                      kind: TypeDefKind.StringKind,
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
                kind: TypeDefKind.ObjectKind,
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
              returnType: { kind: TypeDefKind.StringKind },
              description: "",
              args: [
                {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              ],
            },
            {
              name: "helloWorld",
              returnType: { kind: TypeDefKind.StringKind },
              description: "",
              args: [
                {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
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
                kind: TypeDefKind.ListKind,
                typeDef: {
                  kind: TypeDefKind.StringKind,
                },
              },
              description: "packages to install",
            },
            {
              name: "ctr",
              typeDef: {
                kind: TypeDefKind.ObjectKind,
                name: "Container",
              },
              description: "",
            },
          ],
          methods: [
            {
              name: "base",
              returnType: {
                kind: TypeDefKind.ObjectKind,
                name: "Alpine",
              },
              description: "Returns a base Alpine container",
              args: [
                {
                  name: "version",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "version to use (default to: 3.16.2)",
                  optional: true,
                  defaultValue: undefined,
                },
              ],
            },
            {
              name: "install",
              returnType: {
                kind: TypeDefKind.ObjectKind,
                name: "Alpine",
              },
              description: "",
              args: [
                {
                  name: "pkgs",
                  typeDef: {
                    kind: TypeDefKind.ListKind,
                    typeDef: {
                      kind: TypeDefKind.StringKind,
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
              returnType: { kind: TypeDefKind.StringKind },
              description: "",
              args: [
                {
                  name: "cmd",
                  typeDef: {
                    kind: TypeDefKind.ListKind,
                    typeDef: {
                      kind: TypeDefKind.StringKind,
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
              returnType: { kind: TypeDefKind.StringKind },
              description: "",
              args: [
                {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "",
                  optional: true,
                  defaultValue: undefined,
                },
              ],
            },
            {
              name: "isTrue",
              returnType: { kind: TypeDefKind.BooleanKind },
              description: "",
              args: [
                {
                  name: "value",
                  typeDef: { kind: TypeDefKind.BooleanKind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              ],
            },
            {
              name: "add",
              returnType: { kind: TypeDefKind.IntegerKind },
              description: "",
              args: [
                {
                  name: "a",
                  typeDef: { kind: TypeDefKind.IntegerKind },
                  description: "",
                  optional: true,
                  defaultValue: "0",
                },
                {
                  name: "b",
                  typeDef: { kind: TypeDefKind.IntegerKind },
                  description: "",
                  optional: true,
                  defaultValue: "0",
                },
              ],
            },
            {
              name: "sayBool",
              returnType: { kind: TypeDefKind.BooleanKind },
              description: "",
              args: [
                {
                  name: "value",
                  typeDef: { kind: TypeDefKind.BooleanKind },
                  description: "",
                  optional: true,
                  defaultValue: "false",
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
              returnType: { kind: TypeDefKind.VoidKind },
              description: "",
              args: [
                {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              ],
            },
            {
              name: "asyncHelloWorld",
              returnType: { kind: TypeDefKind.VoidKind },
              description: "",
              args: [
                {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
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
                typeDef: { kind: TypeDefKind.StringKind },
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
                kind: TypeDefKind.StringKind,
              },
              description: "",
              args: [
                {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
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
