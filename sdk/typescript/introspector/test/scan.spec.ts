/* eslint-disable @typescript-eslint/no-explicit-any */
import assert from "assert"
import * as fs from "fs"
import { describe, it } from "mocha"
import * as path from "path"
import { fileURLToPath } from "url"

import { scan } from "../scanner/scan.js"
import { listFiles } from "../utils/files.js"

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const rootDirectory = `${__dirname}/testdata`

type TestCase = {
  name: string
  directory: string
}

describe("scan static TypeScript", function () {
  const testCases: TestCase[] = [
    {
      name: "Should correctly scan a basic class with one method",
      directory: "helloWorld",
    },
    {
      name: "Should correctly scan multiple arguments",
      directory: "multiArgs",
    },
    {
      name: "Should supports multiple files and classes that returns classes",
      directory: "multipleObjects",
    },
    {
      name: "Should not expose private methods from a class",
      directory: "privateMethod",
    },
    {
      name: "Should scan classes' properties to keep a state",
      directory: "state",
    },
    {
      name: "Should detect optional parameters of a method",
      directory: "optionalParameter",
    },
    {
      name: "Should correctly handle function with void return",
      directory: "voidReturn",
    },
    {
      name: "Should introspect constructor",
      directory: "constructor",
    },
    {
      name: "Should correctly scan variadic arguments",
      directory: "variadic",
    },
    {
      name: "Should correctly scan alias",
      directory: "alias",
    },
    {
      name: "Should correctly serialize object param",
      directory: "objectParam",
    },
    {
      name: "Should correctly scan multiple objects as fields",
      directory: "multipleObjectsAsFields",
    },
    {
      name: "Should correctly scan scalar arguments",
      directory: "scalar",
    },
    {
      name: "Should correctly scan list of objects",
      directory: "list",
    },
    {
      name: "Should correctly scan enums",
      directory: "enums",
    },
  ]

  for (const test of testCases) {
    it(test.name, async function () {
      const files = await listFiles(`${rootDirectory}/${test.directory}`)
      const result = scan(files, test.directory)
      const jsonResult = JSON.stringify(result, null, 2)
      const expected = fs.readFileSync(
        `${rootDirectory}/${test.directory}/expected.json`,
        "utf-8",
      )

      assert.deepEqual(JSON.parse(jsonResult), JSON.parse(expected))
    })
  }

  describe("Should throw error on invalid module", function () {
    it("Should throw an error when no files are provided", async function () {
      try {
        await scan([], "")
        assert.fail("Should throw an error")
      } catch (e: any) {
        assert.equal(e.message, "no files to introspect found")
      }
    })

    it("Should throw an error if the module is invalid", async function () {
      try {
        const files = await listFiles(`${rootDirectory}/invalid`)

        scan(files, "invalid")
        assert.fail("Should throw an error")
      } catch (e: any) {
        assert.equal(e.message, "no objects found in the module")
      }
    })

    it("Should throw an error if the module class has no decorators", async function () {
      try {
        const files = await listFiles(`${rootDirectory}/noDecorators`)

        scan(files, "noDecorators")
        assert.fail("Should throw an error")
      } catch (e: any) {
        assert.equal(e.message, "no objects found in the module")
      }
    })

    it("Should throw an error if a primitive type is used", async function () {
      try {
        const files = await listFiles(`${rootDirectory}/primitives`)

        const f = scan(files, "primitives")
        // Trigger the module resolution with a strigify
        JSON.stringify(f, null, 2)

        assert.fail("Should throw an error")
      } catch (e: any) {
        assert.equal(
          e.message,
          "Use of primitive String type detected, did you mean string?",
        )
      }
    })
  })
})
