import assert from "assert"
import Client from "../client.gen.js"
import { queryBuilder, queryFlatten } from "../utils.js"

describe("NodeJS SDK api", function () {
  it("Build correctly a query with one argument", async function () {
    const tree = new Client().container().from("alpine")

    assert.strictEqual(
      queryBuilder(tree.queryTree),
      `{container{from(address:"alpine")}}`
    )
  })

  it("Build one query with multiple arguments", async function () {
    const tree = new Client()
      .container()
      .from("alpine")
      .withExec(["apk", "add", "curl"])

    assert.strictEqual(
      queryBuilder(tree.queryTree),
      `{container{from(address:"alpine"){withExec(args:["apk","add","curl"])}}}`
    )
  })

  it("Build a query by splitting it", async function () {
    const image = new Client().container().from("alpine")
    const pkg = image.withExec(["apk", "add", "curl"])

    assert.strictEqual(
      queryBuilder(pkg.queryTree),
      `{container{from(address:"alpine"){withExec(args:["apk","add","curl"])}}}`
    )
  })

  it("Test Field Immutability", async function () {
    const image = new Client().container().from("alpine")
    const a = image.withExec(["echo", "hello", "world"])
    assert.strictEqual(
      queryBuilder(a.queryTree),
      `{container{from(address:"alpine"){withExec(args:["echo","hello","world"])}}}`
    )
    const b = image.withExec(["echo", "foo", "bar"])
    assert.strictEqual(
      queryBuilder(b.queryTree),
      `{container{from(address:"alpine"){withExec(args:["echo","foo","bar"])}}}`
    )
  })

  it("Return a flatten Graphql response", async function () {
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

  it("Return a error for Graphql object nested response", async function () {
    const tree = {
      container: {
        from: "from",
      },
      host: {
        directory: "directory",
      },
    }

    assert.throws(() => queryFlatten(tree), Error(""))
  })
})
