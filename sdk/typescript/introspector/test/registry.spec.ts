import assert from "assert"

import { dag, Container } from "../../api/client.gen.js"
import { connection } from "../../connect.js"
import { Registry } from "../registry/registry.js"

describe("Registry", function () {
  it("Should support function", async function () {
    const registry = new Registry()

    @registry.object
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    class HelloWorld {
      @registry.func
      greeting(name: string): string {
        return `Hello ${name}`
      }
    }

    const result = await registry.getResult(
      "HelloWorld",
      "greeting",
      {},
      {
        name: "world",
      }
    )
    assert.equal(result, "Hello world")
  })

  it("Should support async function", async function () {
    const registry = new Registry()

    @registry.object
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    class HelloWorld {
      @registry.func
      async asyncGreeting(name: string): Promise<string> {
        return `Hello ${name}`
      }
    }

    const result = await registry.getResult(
      "HelloWorld",
      "asyncGreeting",
      {},
      {
        name: "world",
      }
    )
    assert.equal(result, "Hello world")
  })

  it("Should support calling multiple method", async function () {
    const registry = new Registry()

    @registry.object
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    class HelloWorld {
      @registry.func
      async asyncGreeting(name: string): Promise<string> {
        return `Hello ${name}`
      }

      @registry.func
      greeting(name: string): string {
        return `Hello ${name}`
      }
    }

    const resultAsyncGreeting = await registry.getResult(
      "HelloWorld",
      "asyncGreeting",
      {},
      {
        name: "world",
      }
    )
    assert.equal(resultAsyncGreeting, "Hello world")

    const resultGreeting = await registry.getResult(
      "HelloWorld",
      "greeting",
      {},
      {
        name: "world",
      }
    )
    assert.equal(resultGreeting, "Hello world")
  })

  it("Should support initialized state management", async function () {
    const registry = new Registry()

    @registry.object
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    class HelloWorld {
      prefix = "Hello"

      @registry.func
      greeting(name: string): string {
        return `${this.prefix} ${name}`
      }
    }

    const result = await registry.getResult(
      "HelloWorld",
      "greeting",
      {},

      {
        name: "world",
      }
    )
    assert.equal(result, "Hello world")
  })

  it("Should support dynamic state management", async function () {
    const registry = new Registry()

    @registry.object
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    class HelloWorld {
      @registry.field
      prefix = "placeholder"

      @registry.func
      greeting(name: string): string {
        return `${this.prefix} ${name}`
      }
    }

    const result = await registry.getResult(
      "HelloWorld",
      "greeting",
      {
        prefix: "Hey",
      },
      {
        name: "world",
      }
    )

    assert.equal(result, "Hey world")
  })

  it("Should support returning self", async function () {
    const registry = new Registry()

    @registry.object
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    class HelloWorld {
      @registry.field
      prefix = "placeholder"

      @registry.func
      greeting(): HelloWorld {
        this.prefix = "self"

        return this
      }
    }

    const result = await registry.getResult("HelloWorld", "greeting", {}, {})

    assert.deepEqual(result, { prefix: "self" })
  })

  it("Should support object as argument", async function () {
    class Ctr {
      id: string

      constructor(id: string) {
        this.id = id
      }
    }

    const ctr = new Ctr("1")

    const registry = new Registry()

    @registry.object
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    class HelloWorld {
      @registry.field
      ctr?: Ctr = undefined

      @registry.func
      container(ctr: Ctr): HelloWorld {
        this.ctr = ctr

        return this
      }
    }

    const result = await registry.getResult(
      "HelloWorld",
      "container",
      {},
      {
        ctr: ctr,
      }
    )

    assert.deepEqual(result, { ctr: { id: "1" } })
  })

  it("Should supports multiple arguments", async function () {
    const registry = new Registry()

    @registry.object
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    class HelloWorld {
      @registry.func
      compute(a: number, b: number, c: number): number {
        return a * b + c
      }
    }

    const result = await registry.getResult(
      "HelloWorld",
      "compute",
      {},
      {
        // Send argument in disorder to ensure we order them back
        a: 1,
        b: 2,
        c: 3,
      }
    )

    assert.equal(result, 5)
  })

  it("Should correctly serialize data", async function () {
    this.timeout(60000)

    const registry = new Registry()

    @registry.object
    class Bar {
      @registry.field
      ctr?: Container = undefined

      @registry.field
      msg = "foobar"

      @registry.func
      async bar(): Promise<string> {
        return (
          (await this.ctr?.withExec(["echo", "-n", this.msg]).stdout()) || ""
        )
      }
    }

    @registry.object
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    class Foo {
      @registry.field
      foo(): Bar {
        const b = new Bar()

        b.ctr = dag.container().from("alpine:3.16.2")
        b.msg = "Hello Dagger"

        return b
      }
    }

    await connection(async () => {
      const fooResult = await registry.getResult("Foo", "foo", {}, {})

      const result = await registry.getResult("Bar", "bar", fooResult, {})

      assert.equal(result, "Hello Dagger")
    })
  })

  it("Should support constructor", async function () {
    this.timeout(60000)

    const registry = new Registry()

    @registry.object
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    class HelloWorld {
      @registry.field
      msg: string

      constructor(msg?: string) {
        this.msg = msg || "world"
      }

      @registry.func
      sayHi(): string {
        return `Hello ${this.msg}`
      }
    }

    const constructorResult = await registry.getResult(
      "HelloWorld",
      "",
      {},
      {
        msg: "Dagger",
      }
    )
    assert.deepEqual(constructorResult, { msg: "Dagger" })

    const result = await registry.getResult(
      "HelloWorld",
      "sayHi",
      constructorResult,
      {}
    )
    assert.deepEqual(result, "Hello Dagger")
  })

  it("Should support overriding default arg", async function () {
    this.timeout(60000)

    const registry = new Registry()

    @registry.object
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    class HelloWorld {
      @registry.func
      sayHi(msg = ["foobar"]): string {
        return msg.join(" ")
      }
    }

    const defaultResult = await registry.getResult(
      "HelloWorld",
      "sayHi",
      {},
      {}
    )
    assert.deepEqual(defaultResult, "foobar")

    const result = await registry.getResult(
      "HelloWorld",
      "sayHi",
      {},
      {
        msg: ["hello", "there"],
      }
    )
    assert.deepEqual(result, "hello there")
  })
})
