import assert from "assert"
import * as path from "path"
import { fileURLToPath } from "url"

import { analysis } from "../analysis/analysis.js"
import { Metadata } from "../analysis/metadata.js"
import { listFiles } from "../utils/files.js"
import { HelloWorld } from "./testdata/helloWorld/helloWorld"

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const rootDirectory = `${__dirname}/testdata`

describe("Analysis static Typescript", function () {
  it("Should correctly analyse a basic class with one method", async function () {
    const files = await listFiles(`${rootDirectory}/helloWorld`)

    const result = analysis(files)
    const expected: Metadata = {
      classes: [
        {
          name: "HelloWorld",
          doc: "HelloWorld class",
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

    const result = analysis(files)
    const expected: Metadata = {
      classes: [],
      functions: [],
    }

    assert.deepEqual(result, expected)
  })

  it("Should supports multiple files and classes that returns classes", async function () {
    const files = await listFiles(`${rootDirectory}/multipleObjects`)

    const result = analysis(files)
    const expected: Metadata = {
      classes: [
        {
          name: "Bar",
          doc: "Bar class",
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
                },
              ],
            },
          ],
        },
        {
          name: "Foo",
          doc: "Foo class",
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
})
