import assert from "assert"
import { randomUUID } from "crypto"
import fs from "fs"

import {
  ExecError,
  TooManyNestedObjectsError,
} from "../../common/errors/index.js"
import { buildQuery, queryFlatten } from "../../common/graphql/compute_query.js"
import {
  Client,
  ClientContainerOpts,
  connect,
  Container,
  NetworkProtocol,
} from "../../index.js"

const querySanitizer = (query: string) => query.replace(/\s+/g, " ")

describe("TypeScript SDK api", function () {
  it("Build correctly a query with one argument", function () {
    const tree = new Client().container().from("alpine:3.16.2")

    assert.strictEqual(
      querySanitizer(buildQuery(tree["_ctx"]["_queryTree"])),
      `{ container { from (address: "alpine:3.16.2") } }`,
    )
  })

  it("Build correctly a query with different args type", function () {
    const tree = new Client().container().from("alpine:3.16.2")

    assert.strictEqual(
      querySanitizer(buildQuery(tree["_ctx"]["_queryTree"])),
      `{ container { from (address: "alpine:3.16.2") } }`,
    )

    const tree2 = new Client().git("fake_url", { keepGitDir: true })

    assert.strictEqual(
      querySanitizer(buildQuery(tree2["_ctx"]["_queryTree"])),
      `{ git (url: "fake_url",keepGitDir: true) }`,
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
      `{ test_types (id: 1,platform: ["string","string2"],boolean: true,object: {}) }`,
    )
  })

  it("Build one query with multiple arguments", function () {
    const tree = new Client()
      .container()
      .from("alpine:3.16.2")
      .withExec(["apk", "add", "curl"])

    assert.strictEqual(
      querySanitizer(buildQuery(tree["_ctx"]["_queryTree"])),
      `{ container { from (address: "alpine:3.16.2") { withExec (args: ["apk","add","curl"]) }} }`,
    )
  })

  it("Build a query by splitting it", function () {
    const image = new Client().container().from("alpine:3.16.2")
    const pkg = image.withExec(["echo", "foo bar"])

    assert.strictEqual(
      querySanitizer(buildQuery(pkg["_ctx"]["_queryTree"])),
      `{ container { from (address: "alpine:3.16.2") { withExec (args: ["echo","foo bar"]) }} }`,
    )
  })

  it("Pass a client with an explicit ID as a parameter", async function () {
    this.timeout(60000)
    await connect(async (client: Client) => {
      const image = await client
        .loadContainerFromID(
          await client
            .container()
            .from("alpine:3.16.2")
            .withExec(["apk", "add", "yarn"])
            .id(),
        )
        .withMountedCache("/root/.cache", client.cacheVolume("cache_key"))
        .withExec(["echo", "foo bar"])
        .stdout()

      assert.strictEqual(image, `foo bar\n`)
    })
  })

  it("Pass a cache volume with an implicit ID as a parameter", async function () {
    this.timeout(60000)
    await connect(async (client: Client) => {
      const cacheVolume = client.cacheVolume("cache_key")
      const image = await client
        .container()
        .from("alpine:3.16.2")
        .withExec(["apk", "add", "yarn"])
        .withMountedCache("/root/.cache", cacheVolume)
        .withExec(["echo", "foo bar"])
        .stdout()

      assert.strictEqual(image, `foo bar\n`)
    })
  })

  it("Build a query with positionnal and optionals arguments", function () {
    const image = new Client().container().from("alpine:3.16.2")
    const pkg = image.withExec(["apk", "add", "curl"], {
      experimentalPrivilegedNesting: true,
    })

    assert.strictEqual(
      querySanitizer(buildQuery(pkg["_ctx"]["_queryTree"])),
      `{ container { from (address: "alpine:3.16.2") { withExec (args: ["apk","add","curl"],experimentalPrivilegedNesting: true) }} }`,
    )
  })

  it("Test Field Immutability", async function () {
    const image = new Client().container().from("alpine:3.16.2")
    const a = image.withExec(["echo", "hello", "world"])
    assert.strictEqual(
      querySanitizer(buildQuery(a["_ctx"]["_queryTree"])),
      `{ container { from (address: "alpine:3.16.2") { withExec (args: ["echo","hello","world"]) }} }`,
    )
    const b = image.withExec(["echo", "foo", "bar"])
    assert.strictEqual(
      querySanitizer(buildQuery(b["_ctx"]["_queryTree"])),
      `{ container { from (address: "alpine:3.16.2") { withExec (args: ["echo","foo","bar"]) }} }`,
    )
  })

  it("Test awaited Field Immutability", async function () {
    this.timeout(60000)
    await connect(async (client: Client) => {
      const image = client
        .container()
        .from("alpine:3.16.2")
        .withExec(["echo", "hello", "world"])

      const a = await image.withExec(["echo", "foobar"]).stdout()
      assert.strictEqual(a, "foobar\n")

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
        `,
      )

      const builder = image
        .dockerBuild()
        .withWorkdir("/")
        .withExec(["echo", "htrshtrhrthrts"], { redirectStdout: "file.txt" })

      const copiedFile = await client
        .container()
        .from("alpine:3.16.2")
        .withWorkdir("/")
        .withFile("/copied-file.txt", builder.file("/file.txt"))
        .file("copied-file.txt")
        .contents()

      assert.strictEqual(copiedFile, "htrshtrhrthrts\n")
    })
  })

  it("Return a flatten Graphql response", function () {
    const tree = {
      container: {
        from: {
          withExec: {
            stdout:
              "fetch https://dl-cdn.alpinelinux.org/alpine/v3.16/main/aarch64/APKINDEX.tar.gz",
          },
        },
      },
    }

    assert.deepStrictEqual(
      queryFlatten(tree),
      "fetch https://dl-cdn.alpinelinux.org/alpine/v3.16/main/aarch64/APKINDEX.tar.gz",
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

  it("Return custom ExecError", async function () {
    this.timeout(60000)

    const stdout = "STDOUT HERE"
    const stderr = "STDERR HERE"
    const args = ["sh", "-c", "cat /testout >&1; cat /testerr >&2; exit 127"]

    await connect(async (client: Client) => {
      const ctr = client
        .container()
        .from("alpine:3.16.2")
        .withDirectory(
          "/",
          client
            .directory()
            .withNewFile("testout", stdout)
            .withNewFile("testerr", stderr),
        )
        .withExec(args)

      try {
        await ctr.sync()
      } catch (e) {
        if (e instanceof ExecError) {
          assert(e.message.includes("did not complete successfully"))
          assert.strictEqual(e.exitCode, 127)
          assert.strictEqual(e.stdout, stdout)
          assert.strictEqual(e.stderr, stderr)
          assert(!e.toString().includes(stdout))
          assert(!e.toString().includes(stderr))
          assert(!e.message.includes(stdout))
          assert(!e.message.includes(stderr))
        } else {
          throw e
        }
      }
    })
  })

  it("Support container sync", async function () {
    this.timeout(60000)

    await connect(async (client: Client) => {
      const base = client.container().from("alpine:3.16.2")

      // short circuit
      await assert.rejects(base.withExec(["foobar"]).sync(), ExecError)

      // chaining
      const out = await (
        await base.withExec(["echo", "foobaz"]).sync()
      ).stdout()
      assert.strictEqual(out, "foobaz\n")
    })
  })

  it("Support chainable utils via with()", async function () {
    this.timeout(60000)

    const env = (c: Container): Container => c.withEnvVariable("FOO", "bar")

    const secret = (token: string, client: Client) => {
      return (c: Container): Container =>
        c.withSecretVariable("TOKEN", client.setSecret("TOKEN", token))
    }

    await connect(async (client) => {
      await client
        .container()
        .from("alpine:3.16.2")
        .with(env)
        .with(secret("baz", client))
        .withExec(["sh", "-c", "test $FOO = bar && test $TOKEN = baz"])
        .sync()
    })
  })

  it("Compute nested arguments", async function () {
    const tree = new Client()
      .directory()
      .dockerBuild({ buildArgs: [{ value: "foo", name: "test" }] })

    assert.strictEqual(
      querySanitizer(buildQuery(tree["_ctx"]["_queryTree"])),
      `{ directory { dockerBuild (buildArgs: [{value:"foo",name:"test"}]) } }`,
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
        const seededPlatformVariants: Container[] = []

        for (const platform in platforms) {
          const name = platforms[platform]

          const ctr = client
            .container({ platform } as ClientContainerOpts)
            .from("alpine:3.16.2")
            .withExec(["uname", "-m"])

          const result = await ctr.stdout()
          assert.strictEqual(result.trim(), name)

          seededPlatformVariants.push(ctr)
        }

        const exportID = `export-${randomUUID()}`

        const success = await client.container().export(`./${exportID}`, {
          platformVariants: seededPlatformVariants,
        })

        await fs.unlinkSync(exportID)
        assert.ok(success.endsWith(exportID))
      },
      { LogOutput: process.stderr },
    )
  })

  it("Handle enumeration", async function () {
    this.timeout(60000)

    await connect(async (client) => {
      const ports = await client
        .container()
        .from("alpine:3.16.2")
        .withExposedPort(8000, {
          protocol: NetworkProtocol.Udp,
        })
        .exposedPorts()

      assert.strictEqual(await ports[0].protocol(), NetworkProtocol.Udp)
    })
  })

  it("Handle list of objects", async function () {
    this.timeout(60000)

    await connect(
      async (client) => {
        const ctr = client
          .container()
          .from("alpine:3.16.2")
          .withEnvVariable("FOO", "BAR")
          .withEnvVariable("BAR", "BOOL")

        const envs = await ctr.envVariables()

        assert.strictEqual(await envs[1].name(), "FOO")
        assert.strictEqual(await envs[1].value(), "BAR")

        assert.strictEqual(await envs[2].name(), "BAR")
        assert.strictEqual(await envs[2].value(), "BOOL")
      },
      { LogOutput: process.stderr },
    )
  })

  it("Check conflict with enum", async function () {
    this.timeout(60000)

    await connect(async (client) => {
      const env = await client
        .container()
        .from("alpine:3.16.2")
        .withEnvVariable("FOO", "TCP")
        .envVariable("FOO")

      assert.strictEqual(env, "TCP")
    })
  })
})
