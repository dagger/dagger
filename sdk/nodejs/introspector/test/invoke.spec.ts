import assert from "assert"
import * as path from "path"
import { fileURLToPath } from "url"

import { connection } from "../../connect.js"
import { invoke } from "../../entrypoint/invoke.js"
import { load } from "../../entrypoint/load.js"
import { listFiles } from "../utils/files.js"

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const rootDirectory = `${__dirname}/testdata`

/**
 * These tests are a mimic of what dagger entrypoint should do
 * without the call to the Dagger API (data are mocked instead)
 *
 * The principle behind is exactly the same: we load the files and call a function from it.
 */
describe("Invoke typescript function", function () {
  it("Should correctly invoke hello world", async function () {
    const files = await listFiles(`${rootDirectory}/helloWorld`)

    // Load function
    await load(files)

    // Mocking the fetch from the dagger API
    const input = {
      objectName: "HelloWorld",
      methodName: "helloWorld",
      state: {},
      inputs: { name: "world" },
    }

    const result = await invoke(
      input.objectName,
      input.methodName,
      input.state,
      input.inputs
    )

    // We verify the result, this could be serialized and set using `dag.ReturnValue` as a response
    assert.equal(result, "hello world")
  })

  it("Should correctly execute dagger operation", async function () {
    this.timeout(60000)

    const files = await listFiles(`${rootDirectory}/multipleObjects`)

    // Load function
    await load(files)

    // Mocking the fetch from the dagger API
    const input = {
      objectName: "Bar",
      methodName: "exec",
      state: {},
      inputs: {
        // string[]
        cmd: ["echo", "-n", "hello world"],
      },
    }

    // We wrap the execution into a Dagger connection
    await connection(async () => {
      const result = await invoke(
        input.objectName,
        input.methodName,
        input.state,
        input.inputs
      )

      // We verify the result, this could be serialized and set using `dag.ReturnValue` as a response
      assert.equal(result, "hello world")
    })
  })

  it("Should correctly transfer state", async function () {
    this.timeout(60000)

    const files = await listFiles(`${rootDirectory}/state`)

    // Load function
    await load(files)

    // We wrap the execution into a Dagger connection
    await connection(
      async () => {
        // Mocking the fetch from the dagger API
        const inputBase = {
          objectName: "Alpine",
          methodName: "base",
          state: {},
          inputs: { version: ["3.16.0"] },
        }

        const inputBaseResult = await invoke(
          inputBase.objectName,
          inputBase.methodName,
          inputBase.state,
          inputBase.inputs
        )

        const inputInstall = {
          objectName: "Alpine",
          methodName: "install",
          // Would be fetched from dagger and loaded from the container ID
          state: inputBaseResult,
          inputs: {
            pkgs: ["jq"],
          },
        }

        const inputInstallResult = await invoke(
          inputInstall.objectName,
          inputInstall.methodName,
          // Would be fetched from dagger and loaded from the container ID
          inputInstall.state,
          inputInstall.inputs
        )

        const inputExec = {
          objectName: "Alpine",
          methodName: "exec",
          // Would be fetched from dagger and loaded from the container ID
          state: inputInstallResult,
          inputs: {
            cmd: ["jq", "-h"],
          },
        }

        const result = await invoke(
          inputExec.objectName,
          inputExec.methodName,
          inputExec.state,
          inputExec.inputs
        )

        // We verify the result, this could be serialized and set using `dag.ReturnValue` as a response
        // In that case, we verify it's not failing and that it returned a value
        assert.notEqual("", result)
      },
      { LogOutput: process.stderr }
    )
  })
})
