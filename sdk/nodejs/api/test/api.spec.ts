import assert from "assert"
import { randomUUID } from "crypto"
import fs from "fs"

import { TooManyNestedObjectsError } from "../../common/errors/index.js"
import Client, {
  connect,
  ClientContainerOpts,
  Container,
  Directory,
} from "../../index.js"
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

    const tree2 = new Client().git("fake_url", { keepGitDir: true })

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

  it("Pass a client with an explicit ID as a parameter", async function () {
    this.timeout(60000)
    connect(async (client: Client) => {
      const image = await client
        .container({
          id: await client
            .container()
            .from("alpine")
            .withExec(["apk", "add", "yarn"])
            .id(),
        })
        .withMountedCache("/root/.cache", client.cacheVolume("cache_key"))
        .withExec(["echo", "foo bar"])
        .stdout()

      assert.strictEqual(image, `foo  bar`)
    })
  })

  it("Pass a cache volume with an implicit ID as a parameter", async function () {
    this.timeout(60000)
    connect(async (client: Client) => {
      const cacheVolume = client.cacheVolume("cache_key")
      const image = await client
        .container()
        .from("alpine")
        .withExec(["apk", "add", "yarn"])
        .withMountedCache("/root/.cache", cacheVolume)
        .withExec(["echo", "foo bar"])
        .stdout()

      assert.strictEqual(image, `foo  bar`)
    })
  })

  it("Build a query with positionnal and optionals arguments", function () {
    const image = new Client().container().from("alpine")
    const pkg = image.withExec(["apk", "add", "curl"], {
      experimentalPrivilegedNesting: true,
    })

    assert.strictEqual(
      querySanitizer(buildQuery(pkg.queryTree)),
      `{ container { from (address: "alpine") { withExec (args: ["apk","add","curl"],experimentalPrivilegedNesting: true) }} }`
    )
  })

  it("Test Field Immutability", async function () {
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

  it("Test awaited Field Immutability", async function () {
    this.timeout(60000)
    await connect(async (client: Client) => {
      const image = client
        .container()
        .from("alpine")
        .withExec(["echo", "hello", "world"])

      const a = await image.exitCode()
      assert.strictEqual(a, 0)

      const b = await image.stdout()
      assert.strictEqual(b, "hello world\n")
    })
  })

  it("Recursively solve sub queries", async function () {
    this.timeout(60000)

    await connect(async (client) => {
      const image = client.directory().withNewFile(
        "Dockerfile",
        `
            FROM alpine    
        `
      )

      const builder = client
        .container()
        .build(image)
        .withWorkdir("/")
        .withEntrypoint(["sh", "-c"])
        .withExec(["echo htrshtrhrthrts > file.txt"])
        .withExec(["cat file.txt"])

      const copiedFile = await client
        .container()
        .from("alpine")
        .withWorkdir("/")
        .withFile("/copied-file.txt", builder.file("/file.txt"))
        .withEntrypoint(["sh", "-c"])
        .withExec(["cat copied-file.txt"])
        .file("copied-file.txt")
        .contents()

      assert.strictEqual(copiedFile, "htrshtrhrthrts\n")
    })
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

    assert.throws(() => queryFlatten(tree), TooManyNestedObjectsError)
  })

  it("Support chainable utils via with()", function () {
    function AddAFewMounts(c: Container): Container {
      return c
        .withMountedDirectory("/foo", new Client().host().directory("foo"))
        .withMountedDirectory("/bar", new Client().host().directory("bar"))
    }

    const tree = new Client()
      .container()
      .from("alpine")
      .withWorkdir("/foo")
      .with(AddAFewMounts)
      .withExec(["blah"])

    assert.strictEqual(
      querySanitizer(buildQuery(tree.queryTree)),
      `{ container { from (address: "alpine") { withWorkdir (path: "/foo") { withMountedDirectory (path: "/foo",source: {"_queryTree":[{operation:"host"},{operation:"directory",args:{path:"foo"}}],clientHost:"127.0.0.1:8080",sessionToken:"",client:{url:"http://127.0.0.1:8080/query",requestConfig:{headers:{Authorization:"Basic Og=="}}}}) { withMountedDirectory (path: "/bar",source: {"_queryTree":[{operation:"host"},{operation:"directory",args:{path:"bar"}}],clientHost:"127.0.0.1:8080",sessionToken:"",client:{url:"http://127.0.0.1:8080/query",requestConfig:{headers:{Authorization:"Basic Og=="}}}}) { withExec (args: ["blah"]) }}}}} }`
    )
  })

  it("Support chainable utils via with() 2", async function () {
    this.timeout(60000)

    await connect(async (client) => {
      const alpine = client
        .container()
        .from("alpine:3.16.2")
        .with((c: Container): Container => c.withEnvVariable("FOO", "bar"))

      let out = await alpine.withExec(["printenv", "FOO"]).stdout()
      assert.strictEqual(out, "bar\n")

      const withFood = (c: Container): Container =>
        c.withEnvVariable("FOOD", "bar")
      out = await client
        .container()
        .from("alpine:3.16.2")
        .with(withFood)
        .withExec(["printenv", "FOOD"])
        .stdout()

      assert.strictEqual(out, "bar\n")

      const withEnv = (
        env: string,
        value: string
      ): ((c: Container) => Container) => {
        return (c: Container): Container => c.withEnvVariable(env, value)
      }

      out = await client
        .container()
        .from("alpine:3.16.2")
        .with(withEnv("HELLO", "WORLD"))
        .withExec(["printenv", "HELLO"])
        .stdout()
      assert.strictEqual(out, "WORLD\n")
    })
  })

  it("Compute nested arguments", async function () {
    const tree = new Client()
      .container()
      .build(new Directory(), { buildArgs: [{ value: "foo", name: "test" }] })

    assert.strictEqual(
      querySanitizer(buildQuery(tree.queryTree)),
      `{ container { build (context: {"_queryTree":[],clientHost:"127.0.0.1:8080",sessionToken:"",client:{url:"http://undefined/query",requestConfig:{headers:{Authorization:"Basic dW5kZWZpbmVkOg=="}}}},buildArgs: [{value:"foo",name:"test"}]) } }`
    )
  })

  it("Compute empty string value", async function () {
    this.timeout(60000)

    await connect(async (client) => {
      const alpine = client
        .container()
        .from("alpine:3.16.2")
        .withEnvVariable("FOO", "")

      const out = await alpine.withExec(["printenv", "FOO"]).stdout()
      assert.strictEqual(out, "\n")
    })
  })

  it("Compute nested array of arguments", async function () {
    this.timeout(60000)

    const platforms: Record<string, string> = {
      "linux/amd64": "x86_64",
      "linux/arm64": "aarch64",
    }

    await connect(
      async (client) => {
        const seededPlatformVariants = []

        for (const platform in platforms) {
          const name = platforms[platform]

          const ctr = client
            .container({ platform } as ClientContainerOpts)
            .from("alpine:3.16.2")
            .withExec(["uname", "-m"])

          const result = await ctr.stdout()
          assert.strictEqual(result.trim(), name)

          console.log(result)
          seededPlatformVariants.push(ctr)
        }

        const exportID = `./export-${randomUUID()}`

        const isSuccess = await client.container().export(exportID, {
          platformVariants: seededPlatformVariants,
        })

        await fs.unlinkSync(exportID)
        assert.strictEqual(isSuccess, true)
      },
      { LogOutput: process.stderr }
    )
  })
})
