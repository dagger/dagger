import assert from "assert"
import { describe, it } from "mocha"
import * as path from "path"
import * as fs from "fs"
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
      name: "Should ignore class that does not have the object decorator",
      directory: "noDecorators",
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
  ]

  for (const test of testCases) {
    it(test.name, async function () {
      const files = await listFiles(`${rootDirectory}/${test.directory}`)
      const result = scan(files, test.directory)
      const jsonResult = JSON.stringify(result, null, 2)
      const expected = fs.readFileSync(`${rootDirectory}/${test.directory}/expected.json`, "utf-8")

      assert.deepEqual(JSON.parse(jsonResult), JSON.parse(expected))
    })
  }
})
