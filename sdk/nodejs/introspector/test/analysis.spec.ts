import assert from "assert"
import * as path from "path"
import { fileURLToPath } from "url"

import { Metadata } from "../scanner/metadata.js"
import { scan } from "../scanner/scan.js"
import { listFiles } from "../utils/files.js"

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const rootDirectory = `${__dirname}/testdata`

describe("scan static Typescript", function () {
  it("Should correctly scan a basic class with one method", async function () {
    const files = await listFiles(`${rootDirectory}/helloWorld`)

    const result = scan(files)
    const expected: Metadata = {
      classes: [
        {
          name: "HelloWorld",
          doc: "HelloWorld class",
          properties: [],
          methods: [
            {
              name: "helloWorld",
              returnType: "string",
              doc: "",
              params: [
                {
                  name: "name",
                  typeName: "string",
                  doc: "",
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
    const expected: Metadata = {
      classes: [],
      functions: [],
    }

    assert.deepEqual(result, expected)
  })

  it("Should supports multiple files and classes that returns classes", async function () {
    const files = await listFiles(`${rootDirectory}/multipleObjects`)

    const result = scan(files)
    const expected: Metadata = {
      classes: [
        {
          name: "Bar",
          doc: "Bar class",
          properties: [],
          methods: [
            {
              name: "exec",
              doc: "Execute the command and return its result",
              returnType: "string",
              params: [
                {
                  name: "cmd",
                  typeName: "string[]",
                  doc: "Command to execute",
                  optional: false,
                  defaultValue: undefined,
                },
              ],
            },
          ],
        },
        {
          name: "Foo",
          doc: "Foo class",
          properties: [],
          methods: [
            {
              name: "bar",
              doc: "Return Bar object",
              returnType: "Bar",
              params: [],
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
    const expected: Metadata = {
      classes: [
        {
          name: "HelloWorld",
          doc: "HelloWorld class",
          properties: [],
          methods: [
            {
              name: "greeting",
              returnType: "string",
              doc: "",
              params: [
                {
                  name: "name",
                  typeName: "string",
                  doc: "",
                  optional: false,
                  defaultValue: undefined,
                },
              ],
            },
            {
              name: "helloWorld",
              returnType: "string",
              doc: "",
              params: [
                {
                  name: "name",
                  typeName: "string",
                  doc: "",
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
    const expected: Metadata = {
      classes: [
        {
          name: "Alpine",
          doc: "Alpine module",
          properties: [
            {
              name: "packages",
              typeName: "string[]",
              doc: "packages to install",
            },
            {
              name: "ctr",
              typeName: "Container",
              doc: "",
            },
          ],
          methods: [
            {
              name: "base",
              returnType: "Alpine",
              doc: "Returns a base Alpine container",
              params: [
                {
                  name: "version",
                  typeName: "string",
                  doc: "version to use (default to: 3.16.2)",
                  optional: true,
                  defaultValue: undefined,
                },
              ],
            },
            {
              name: "install",
              returnType: "Alpine",
              doc: "",
              params: [
                {
                  name: "pkgs",
                  typeName: "string[]",
                  doc: "",
                  optional: false,
                  defaultValue: undefined,
                },
              ],
            },
            {
              name: "exec",
              returnType: "string",
              doc: "",
              params: [
                {
                  name: "cmd",
                  typeName: "string[]",
                  doc: "",
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
    const expected: Metadata = {
      classes: [
        {
          name: "HelloWorld",
          doc: "HelloWorld class",
          properties: [],
          methods: [
            {
              name: "helloWorld",
              returnType: "string",
              doc: "",
              params: [
                {
                  name: "name",
                  typeName: "string",
                  doc: "",
                  optional: true,
                  defaultValue: undefined,
                },
              ],
            },
            {
              name: "isTrue",
              returnType: "boolean",
              doc: "",
              params: [
                {
                  name: "value",
                  typeName: "boolean",
                  doc: "",
                  optional: false,
                  defaultValue: undefined,
                },
              ],
            },
            {
              name: "add",
              returnType: "number",
              doc: "",
              params: [
                {
                  name: "a",
                  typeName: "number",
                  doc: "",
                  optional: true,
                  defaultValue: "0",
                },
                {
                  name: "b",
                  typeName: "number",
                  doc: "",
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
    const expected: Metadata = {
      classes: [
        {
          name: "HelloWorld",
          doc: "HelloWorld class",
          properties: [],
          methods: [
            {
              name: "helloWorld",
              returnType: "void",
              doc: "",
              params: [
                {
                  name: "name",
                  typeName: "string",
                  doc: "",
                  optional: false,
                  defaultValue: undefined,
                },
              ],
            },
            {
              name: "asyncHelloWorld",
              returnType: "void",
              doc: "",
              params: [
                {
                  name: "name",
                  typeName: "string",
                  doc: "",
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
})
