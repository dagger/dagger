import assert from "assert"
import Client, { connect } from "../../index.js"
import { queryFlatten, buildQuery } from "../utils.js"

const querySanitizer = (query: string) => query.replace(/\s+/g, " ")

describe("NodeJS SDK api", function () {
  it("Build correctly a query with one argument", function () {
    const tree = new Client().container().from("alpine")

    assert.strictEqual(
      querySanitizer(buildQuery(tree.queryTree)),
      `{ container { from (address: "alpine") } }`
    )
  })

  it("Build correctly a query with different args type", function () {
    const tree = new Client().container().from("alpine")

    assert.strictEqual(
      querySanitizer(buildQuery(tree.queryTree)),
      `{ container { from (address: "alpine") } }`
    )

    const tree2 = new Client().git("fake_url", true)

    assert.strictEqual(
      querySanitizer(buildQuery(tree2.queryTree)),
      `{ git (url: "fake_url",keepGitDir: true) }`
    )

    const tree3 = [
      {
        operation: "test_types",
        args: {
          id: 1,
          platform: ["string", "string2"],
          boolean: true,
          object: {},
          undefined: undefined,
        },
      },
    ]

    assert.strictEqual(
      querySanitizer(buildQuery(tree3)),
      `{ test_types (id: 1,platform: ["string","string2"],boolean: true,object: {}) }`
    )
  })

  it("Build one query with multiple arguments", function () {
    const tree = new Client()
      .container()
      .from("alpine")
      .withExec(["apk", "add", "curl"])

    assert.strictEqual(
      querySanitizer(buildQuery(tree.queryTree)),
      `{ container { from (address: "alpine") { withExec (args: ["apk","add","curl"]) }} }`
    )
  })

  it("Build a query by splitting it", function () {
    const image = new Client().container().from("alpine")
    const pkg = image.withExec(["echo", "foo bar"])

    assert.strictEqual(
      querySanitizer(buildQuery(pkg.queryTree)),
      `{ container { from (address: "alpine") { withExec (args: ["echo","foo bar"]) }} }`
    )
  })

  it("Pass a client with an implicit ID as a parameter", async function () {
    connect(async (client: Client) => {
      const image = await client
        .container(
          client.container().from("alpine").withExec(["apk", "add", "yarn"])
        )
        .withMountedCache("/root/.cache", client.cacheVolume("cache_key"))
        .withExec(["echo", "foo bar"])
        .stdout()

      assert.strictEqual(image, `foo  bar`)
    })
  })

  it("Test Field Immutability", function () {
    const image = new Client().container().from("alpine")
    const a = image.withExec(["echo", "hello", "world"])
    assert.strictEqual(
      querySanitizer(buildQuery(a.queryTree)),
      `{ container { from (address: "alpine") { withExec (args: ["echo","hello","world"]) }} }`
    )
    const b = image.withExec(["echo", "foo", "bar"])
    assert.strictEqual(
      querySanitizer(buildQuery(b.queryTree)),
      `{ container { from (address: "alpine") { withExec (args: ["echo","foo","bar"]) }} }`
    )
  })

  it("Return a flatten Graphql response", function () {
    const tree = {
      container: {
        from: {
          exec: {
            stdout:
              "fetch https://dl-cdn.alpinelinux.org/alpine/v3.16/main/aarch64/APKINDEX.tar.gz",
          },
        },
      },
    }

    assert.deepStrictEqual(
      queryFlatten(tree),
      "fetch https://dl-cdn.alpinelinux.org/alpine/v3.16/main/aarch64/APKINDEX.tar.gz"
    )
  })

  it("Return a error for Graphql object nested response", function () {
    const tree = {
      container: {
        from: "from",
      },
      host: {
        directory: "directory",
      },
    }

    assert.throws(
      () => queryFlatten(tree),
      Error("Too many Graphql nested objects")
    )
  })
})
