import assert from "assert"
import { describe, it } from "mocha"
import Module from "node:module"
import * as path from "path"
import { fileURLToPath } from "url"

import { connection } from "../../connect.js"
import { InvokeCtx } from "../../entrypoint/context.js"
import { invoke } from "../../entrypoint/invoke.js"
import { load } from "../../entrypoint/load.js"
import { Executor } from "../executor/executor.js"
import { scan } from "../scanner/scan.js"
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
    this.timeout(60000)

    const files = await listFiles(`${rootDirectory}/helloWorld`)

    // Load function
    const modules = await load(files)
    const executor = new Executor(modules)

    const scanResult = scan(files)

    // Mocking the fetch from the dagger API
    const input = {
      parentName: "HelloWorld",
      fnName: "helloWorld",
      parentArgs: {},
      fnArgs: { name: "world" },
    }

    const result = await invoke(executor, scanResult, input)

    // We verify the result, this could be serialized and set using `dag.ReturnValue` as a response
    assert.equal(result, "hello world")
  })

  it("Should correctly execute dagger operation", async function () {
    this.timeout(60000)

    const files = await listFiles(`${rootDirectory}/multipleObjects`)

    // Load function
    const modules = await load(files)
    const executor = new Executor(modules)

    const scanResult = scan(files)

    // Mocking the fetch from the dagger API
    const input = {
      parentName: "Bar",
      fnName: "exec",
      parentArgs: {},
      fnArgs: {
        // string[]
        cmd: ["echo", "-n", "hello world"],
      },
    }

    // We wrap the execution into a Dagger connection
    await connection(async () => {
      const result = await invoke(executor, scanResult, input)

      // We verify the result, this could be serialized and set using `dag.ReturnValue` as a response
      assert.equal(result, "hello world")
    })
  })

  it("Should correctly order arguments", async function () {
    this.timeout(60000)

    const files = await listFiles(`${rootDirectory}/multiArgs`)

    // Load function
    const modules = await load(files)
    const executor = new Executor(modules)

    const scanResult = scan(files)

    // Mocking the fetch from the dagger API
    const input = {
      parentName: "MultiArgs",
      fnName: "compute",
      parentArgs: {},
      fnArgs: {
        b: 2,
        a: 4,
        c: 3,
      },
    }

    // We wrap the execution into a Dagger connection
    await connection(async () => {
      const result = await invoke(executor, scanResult, input)

      // We verify the result
      assert.equal(result, 11)
    })
  })

  it("Should correctly transfer state", async function () {
    this.timeout(60000)

    const files = await listFiles(`${rootDirectory}/state`)

    // Load function
    const modules = await load(files)
    const executor = new Executor(modules)

    const scanResult = scan(files)

    // We wrap the execution into a Dagger connection
    await connection(
      async () => {
        // Mocking the fetch from the dagger API
        const inputBase = {
          parentName: "State",
          fnName: "base",
          parentArgs: {
            version: "3.16.2",
            user: "root",
            packages: [],
          },
          fnArgs: { version: "3.16.0" },
        }

        const inputBaseResult = await invoke(executor, scanResult, inputBase)

        // Assert state has been updated by the function
        assert.equal("3.16.0", inputBaseResult.version)
        assert.equal("root", inputBaseResult.user)
        assert.deepEqual([], inputBaseResult.packages)
        assert.notEqual(undefined, inputBaseResult.ctr)

        const inputInstall = {
          parentName: "State",
          fnName: "install",
          // Would be fetched from dagger and parsed from dagger entrypoint
          parentArgs: JSON.parse(JSON.stringify(inputBaseResult)),
          fnArgs: {
            pkgs: ["jq"],
          },
        }

        const inputInstallResult = await invoke(
          executor,
          scanResult,
          inputInstall,
        )

        // Verify state conservation
        assert.equal("3.16.0", inputInstallResult.version)
        assert.equal("root", inputInstallResult.user)
        assert.deepEqual(["jq"], inputInstallResult.packages)
        assert.notEqual(undefined, inputInstallResult.ctr)

        const inputExec = {
          parentName: "State",
          fnName: "exec",
          // Would be fetched from dagger and parsed from dagger entrypoint
          parentArgs: JSON.parse(JSON.stringify(inputInstallResult)),
          fnArgs: {
            cmd: ["jq", "-h"],
          },
        }

        const result = await invoke(executor, scanResult, inputExec)

        // We verify the result, this could be serialized and set using `dag.ReturnValue` as a response
        // In that case, we verify it's not failing and that it returned a value
        assert.notEqual("", result)
      },
      { LogOutput: process.stderr },
    )
  })

  it("Should correctly handle multiple objects as fields", async function () {
    this.timeout(60000)

    const files = await listFiles(`${rootDirectory}/multipleObjectsAsFields`)

    // Load function
    const modules = await load(files)
    const executor = new Executor(modules)

    const scanResult = scan(files)

    const constructorInput = {
      parentName: "MultipleObjectsAsFields",
      fnName: "", // call constructor
      parentArgs: {},
      fnArgs: {},
    }

    const constructorResult = await invoke(
      executor,
      scanResult,
      constructorInput,
    )
    // Verify object instantiation
    assert.notStrictEqual(undefined, constructorResult)
    assert.notStrictEqual(undefined, constructorResult.test)
    assert.notStrictEqual(undefined, constructorResult.lint)

    // Call echo method
    const invokeTestEcho = {
      parentName: "Test",
      fnName: "echo",
      parentArgs: {},
      fnArgs: {},
    }

    const testEchoResult = await invoke(executor, scanResult, invokeTestEcho)
    assert.strictEqual("world", testEchoResult)

    // Call echo method
    const invokeLintEcho = {
      parentName: "Lint",
      fnName: "echo",
      parentArgs: {},
      fnArgs: {},
    }

    const lintEchoResult = await invoke(executor, scanResult, invokeLintEcho)
    assert.strictEqual("world", lintEchoResult)
  })

  describe("Should correctly invoke variadic functions", async function () {
    type Case = {
      [name: string]: { ctx: InvokeCtx; expected: string | number }
    }

    const cases: Case = {
      "invoke full variadic string function": {
        expected: "hello hello world",
        ctx: {
          parentName: "Variadic",
          fnName: "fullVariadicStr",
          parentArgs: {},
          fnArgs: {
            vars: ["hello", "world"],
          },
        },
      },
      "invoke variadic function with fixed first argument": {
        expected: "hello hello+world",
        ctx: {
          parentName: "Variadic",
          fnName: "semiVariadicStr",
          parentArgs: {},
          fnArgs: {
            separator: "+",
            vars: ["hello", "world"],
          },
        },
      },
      "invoke full variadic number function": {
        expected: 3,
        ctx: {
          parentName: "Variadic",
          fnName: "fullVariadicNum",
          parentArgs: {},
          fnArgs: {
            vars: [1, 2],
          },
        },
      },
      "only invoke variadic function with fixed first argument": {
        expected: 12,
        ctx: {
          parentName: "Variadic",
          fnName: "semiVariadicNum",
          parentArgs: {},
          fnArgs: {
            mul: 2,
            vars: [1, 1, 1, 2, 1], // 6
          },
        },
      },
    }

    for (const [name, { ctx, expected }] of Object.entries(cases)) {
      it(name, async function () {
        this.timeout(60000)

        const files = await listFiles(`${rootDirectory}/variadic`)

        // Load function
        const modules = await load(files)
        const executor = new Executor(modules)

        const scanResult = scan(files)

        // We wrap the execution into a Dagger connection
        await connection(async () => {
          const result = await invoke(executor, scanResult, ctx)

          // We verify the result
          assert.equal(result, expected)
        })
      })
    }
  })

  describe("Should correctly handle aliases", async function () {
    // Mocking the fetch from the dagger API
    it("Should correctly invoke hello world", async function () {
      this.timeout(60000)

      const files = await listFiles(`${rootDirectory}/alias`)

      // Load function
      const modules = await load(files)
      const executor = new Executor(modules)

      const scanResult = scan(files)

      // We wrap the execution into a Dagger connection
      await connection(async () => {
        const constructorInput = {
          parentName: "Alias", // Class name
          fnName: "", // call constructor
          parentArgs: {
            prefix: "test",
          },
          fnArgs: {},
        }

        const constructorResult = await invoke(
          executor,
          scanResult,
          constructorInput,
        )

        assert.equal("test", constructorResult.prefix)
        assert.notStrictEqual(undefined, constructorResult.container)

        // Mocking the fetch from the dagger API
        const input = {
          parentName: "Alias", // Class name
          fnName: "greet", // helloWorld
          parentArgs: JSON.parse(JSON.stringify(constructorResult)),
          fnArgs: { name: "Dagger" },
        }

        const result = await invoke(executor, scanResult, input)
        assert.equal("hello Dagger", result)
      })
    })

    it("Should correctly invoke hello world with custom prefix", async function () {
      this.timeout(60000)

      const files = await listFiles(`${rootDirectory}/alias`)

      // Load function
      const modules = await load(files)
      const executor = new Executor(modules)

      const scanResult = scan(files)
      await connection(async () => {
        const constructorInput = {
          parentName: "Alias", // class name
          fnName: "", // call constructor
          parentArgs: {
            prefix: "test",
          },
          fnArgs: {},
        }

        const constructorResult = await invoke(
          executor,
          scanResult,
          constructorInput,
        )

        assert.equal("test", constructorResult.prefix)
        assert.notStrictEqual(undefined, constructorResult.container)

        // Mocking the fetch from the dagger API
        const input = {
          parentName: "Alias", // class name
          fnName: "customGreet", // helloWorld
          parentArgs: JSON.parse(JSON.stringify(constructorResult)),
          fnArgs: { name: "Dagger" },
        }

        const result = await invoke(executor, scanResult, input)
        assert.equal("test Dagger", result)
      })
    })
  })

  describe("Should correctly handle optional arguments", async function () {
    it("Should correctly use default and nullable values", async function () {
      this.timeout(60000)

      const files = await listFiles(`${rootDirectory}/optionalParameter`)

      // Load function
      const modules = await load(files)
      const executor = new Executor(modules)

      const scanResult = scan(files)

      // Mocking the fetch from the dagger API
      const input = {
        parentName: "OptionalParameter",
        fnName: "foo",
        parentArgs: {},
        fnArgs: { a: "foo" },
      }

      const result = await invoke(executor, scanResult, input)

      // We verify the result, this could be serialized and set using `dag.ReturnValue` as a response
      assert.equal(result, `"foo", null, , "foo", null, "bar"`)
    })

    it("Should correctly use overwritten values", async function () {
      this.timeout(60000)

      const files = await listFiles(`${rootDirectory}/optionalParameter`)

      // Load function
      const modules = await load(files)
      const executor = new Executor(modules)

      const scanResult = scan(files)

      // Mocking the fetch from the dagger API
      const input = {
        parentName: "OptionalParameter",
        fnName: "foo",
        parentArgs: {},
        fnArgs: {
          a: "foo",
          c: "ho",
          e: "baz",
          d: "ah",
          f: null,
        },
      }

      const result = await invoke(executor, scanResult, input)

      // We verify the result, this could be serialized and set using `dag.ReturnValue` as a response
      assert.equal(result, `"foo", null, "ho", "ah", "baz", null`)
    })
  })

  it("Should correctly handle object arguments", async function () {
    this.timeout(60000)

    const files = await listFiles(`${rootDirectory}/objectParam`)

    // Load function
    const modules = await load(files)
    const executor = new Executor(modules)

    const scanResult = scan(files)

    const inputUpper = {
      parentName: "ObjectParam",
      fnName: "upper",
      parentArgs: {},
      fnArgs: {
        msg: { content: "hello world" },
      },
    }

    const resultUpper = await invoke(executor, scanResult, inputUpper)

    // We verify the result, this could be serialized and set using `dag.ReturnValue` as a response
    assert.equal(resultUpper.content, "HELLO WORLD")

    const inputUppers = {
      parentName: "ObjectParam",
      fnName: "uppers",
      parentArgs: {},
      fnArgs: {
        msg: [
          { content: "hello world" },
          { content: "hello Dagger" },
          { content: "hello Universe" },
        ],
      },
    }

    const resultUppers = await invoke(executor, scanResult, inputUppers)

    // We verify the result, this could be serialized and set using `dag.ReturnValue` as a response
    assert.deepEqual(resultUppers, [
      { content: "HELLO WORLD" },
      { content: "HELLO DAGGER" },
      { content: "HELLO UNIVERSE" },
    ])
  })

  it("Should correctly handle list of returned object", async function () {
    this.timeout(60000)

    const files = await listFiles(`${rootDirectory}/list`)

    // Load function
    const modules = await load(files)
    const executor = new Executor(modules)

    const scanResult = scan(files)

    const input = {
      parentName: "List",
      fnName: "create",
      parentArgs: {},
      fnArgs: {
        n: [-1, 2, 3],
      },
    }

    const resultList = await invoke(executor, scanResult, input)

    assert.equal(resultList.length, 3)
    assert.deepEqual(resultList, [{ value: -1 }, { value: 2 }, { value: 3 }])
  })

  it("Should correctly handle enums values", async function () {
    this.timeout(60000)

    const files = await listFiles(`${rootDirectory}/enums`)
    let modules: Module[] = []

    // Load function
    try {
      modules = await load(files)
    } catch {
      assert.fail("failed to load files")
    }

    const executor = new Executor(modules)
    const module = scan(files)

    const inputDefault = {
      parentName: "Enums",
      fnName: "getStatus",
      parentArgs: {
        status: "ACTIVE",
      },
      fnArgs: {},
    }

    const resultDefault = await invoke(executor, module, inputDefault)

    assert.equal(resultDefault, "ACTIVE")

    const inputSet = {
      parentName: "Enums", // class name
      fnName: "setStatus", // helloWorld
      parentArgs: {
        status: "ACTIVE",
      },
      fnArgs: {
        status: "INACTIVE",
      },
    }

    const resultSet = await invoke(executor, module, inputSet)

    const inputAfterSet = {
      parentName: "Enums",
      fnName: "getStatus",
      parentArgs: JSON.parse(JSON.stringify(resultSet)),
      fnArgs: {},
    }

    const resultAfterSet = await invoke(executor, module, inputAfterSet)

    assert.equal(resultAfterSet, "INACTIVE")
  })
})
