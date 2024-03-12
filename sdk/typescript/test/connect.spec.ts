import assert, { AssertionError } from "assert"
import * as crypto from "crypto"
import * as fs from "fs"
import * as http from "http"
import { AddressInfo } from "net"
import * as os from "os"
import * as path from "path"
import * as tar from "tar"

import { dag } from "../api/client.gen.js"
import { GraphQLRequestError } from "../common/errors/index.js"
import { connect, close, connection } from "../connect.js"
import * as bin from "../provisioning/bin.js"
import { CLI_VERSION } from "../provisioning/default.js"

describe("TypeScript default client", function () {
  it("Should use the default client and close connection on call to close", async function () {
    this.timeout(60000)

    // Check if the connection is actually not set before calling an execution
    // We verify the lazy evaluation that way
    assert.equal(dag["_ctx"]["_client"], undefined)

    const out = await dag
      .container()
      .from("alpine:3.16.2")
      .withExec(["echo", "hello", "world"])
      .stdout()

    assert.equal(out, "hello world\n")

    // Check if the connection is still up
    assert.notEqual(dag["_ctx"]["_client"], undefined)

    close()

    // Check if the connection has been correctly reset
    assert.equal(dag["_ctx"]["_client"], undefined)
  })

  it("Should automatically close connection", async function () {
    this.timeout(60000)

    // Check if the connection is actually not set before calling connection
    assert.equal(dag["_ctx"]["_client"], undefined)

    await connection(async () => {
      const out = await dag
        .container()
        .from("alpine:3.16.2")
        .withExec(["echo", "hello", "world"])
        .stdout()

      assert.equal(out, "hello world\n")

      // Check if the connection is still up
      assert.notEqual(dag["_ctx"]["_client"], undefined)
    })

    // Check if the connection has been correctly reset
    assert.equal(dag["_ctx"]["_client"], undefined)
  })

  it("Should automatically close connection with config", async function () {
    this.timeout(60000)

    // Check if the connection is actually not set before calling connection
    assert.equal(dag["_ctx"]["_client"], undefined)

    await connection(
      async () => {
        // Check if the connection is up
        assert.notEqual(dag["_ctx"]["_client"], undefined)

        const out = await dag
          .container()
          .from("alpine:3.16.2")
          .withExec(["echo", "hello", "world"])
          .stdout()

        assert.equal(out, "hello world\n")
      },
      { LogOutput: process.stderr },
    )

    // Check if the connection has been correctly reset
    assert.equal(dag["_ctx"]["_client"], undefined)
  })
})

describe("TypeScript sdk Connect", function () {
  it("Should parse DAGGER_SESSION_PORT and DAGGER_SESSION_TOKEN correctly", async function () {
    this.timeout(60000)

    process.env["DAGGER_SESSION_TOKEN"] = "foo"
    process.env["DAGGER_SESSION_PORT"] = "1234"

    await connect(
      async (client) => {
        const authorization = JSON.stringify(
          client["_ctx"]["_client"]?.requestConfig.headers,
        )

        assert.equal(
          // eslint-disable-next-line @typescript-eslint/ban-ts-comment
          // @ts-ignore
          client["_ctx"]["_client"]["url"],
          "http://127.0.0.1:1234/query",
        )
        assert.equal(authorization, `{"Authorization":"Basic Zm9vOg=="}`)
      },
      { LogOutput: process.stderr },
    )

    delete process.env["DAGGER_SESSION_PORT"]
    delete process.env["DAGGER_SESSION_TOKEN"]
  })

  it.skip("Connect to local engine and execute a simple query to make sure it does not fail", async function () {
    this.timeout(60000)

    await connect(
      async (client) => {
        await client
          .container()
          .from("alpine")
          .withExec(["apk", "add", "curl"])
          .withExec(["curl", "https://dagger.io/"])
          .sync()
      },
      { LogOutput: process.stderr },
    )
  })

  it.skip("throws error", async function () {
    this.timeout(60000)

    try {
      await connect(async (client) => {
        await client.container().from("alpine").file("unknown_file").contents()

        assert.fail("Should throw error before reaching this")
      })
    } catch (e) {
      if (e instanceof AssertionError) {
        throw e
      }
      assert(e instanceof GraphQLRequestError)
    }
  })

  describe("Automatic Provisioned CLI Binary", function () {
    let oldEnv: string
    let tempDir: string
    let cacheDir: string

    before(() => {
      oldEnv = JSON.stringify(process.env)
      tempDir = fs.mkdtempSync(path.join(os.tmpdir(), "dagger-test-"))
      cacheDir = fs.mkdtempSync(path.join(os.tmpdir(), "dagger-test-cache"))
      process.env.XDG_CACHE_HOME = cacheDir
    })

    it("Should download and unpack the CLI binary automatically", async function () {
      this.timeout(30000)

      // ignore DAGGER_SESSION_PORT
      delete process.env.DAGGER_SESSION_PORT

      // If explicitly requested to test against a certain URL, use that
      const cliURL = process.env._INTERNAL_DAGGER_TEST_CLI_URL
      if (cliURL) {
        bin._overrideCLIURL(cliURL)
        const checksumsUrl = process.env._INTERNAL_DAGGER_TEST_CLI_CHECKSUMS_URL
        if (!checksumsUrl) {
          throw new Error(
            "Missing override checksums URL when overriding CLI URL",
          )
        }
        bin._overrideCLIChecksumsURL(checksumsUrl)
      }

      // Otherwise if _EXPERIMENTAL_DAGGER_CLI_BIN is set, create a mock http server for it
      const cliBin = process.env._EXPERIMENTAL_DAGGER_CLI_BIN
      if (cliBin && !cliURL) {
        delete process.env._EXPERIMENTAL_DAGGER_CLI_BIN
        // create a temporary dir and write a tar.gz with the cli_bin in it
        const tempArchivePath = path.join(tempDir, "cli.tar.gz")
        // also write the cli bin there in case it's not named "dagger"
        const tempCliBinPath = path.join(tempDir, "dagger")
        fs.writeFileSync(tempCliBinPath, fs.readFileSync(cliBin))
        tar.create(
          {
            gzip: true,
            cwd: tempDir,
            file: tempArchivePath,
            sync: true,
          },
          ["dagger"],
        )

        const archiveName = `dagger_v${CLI_VERSION}_${normalizedOS()}_${normalizedArch()}.tar.gz`

        // calculate the sha256 of the cli archive
        const hash = crypto.createHash("sha256")
        hash.update(fs.readFileSync(tempArchivePath))
        const checksum = hash.digest("hex")
        const checksumFileContents = `${checksum}  ${archiveName}`

        const basePath = `dagger/releases/${CLI_VERSION}`

        const server = http.createServer(
          (req: http.IncomingMessage, res: http.ServerResponse) => {
            if (req.url === `/${basePath}/checksums.txt`) {
              res.writeHead(200, { "Content-Type": "text/plain" })
              res.end(checksumFileContents)
            } else if (req.url === `/${basePath}/${archiveName}`) {
              res.writeHead(200, { "Content-Type": "application/gzip" })
              res.end(fs.readFileSync(tempArchivePath))
            } else {
              res.writeHead(404)
              res.end()
            }
          },
        )

        await new Promise<void>((resolve) => {
          server
            .listen(0, "127.0.0.1", () => {
              const addr = server.address() as AddressInfo
              bin._overrideCLIURL(
                `http://${addr.address}:${addr.port}/${basePath}/${archiveName}`,
              )
              bin._overrideCLIChecksumsURL(
                `http://${addr.address}:${addr.port}/${basePath}/checksums.txt`,
              )
              resolve()
            })
            .unref()
        })
      }

      await connect(
        async (client) => {
          await client.defaultPlatform()
        },
        { LogOutput: process.stderr },
      )
    })

    after(() => {
      process.env = JSON.parse(oldEnv)
      bin._overrideCLIURL("")
      bin._overrideCLIChecksumsURL("")
      fs.rmSync(tempDir, { recursive: true })
      fs.rmSync(cacheDir, { recursive: true })
    })
  })
})

function normalizedArch(): string {
  switch (os.arch()) {
    case "x64":
      return "amd64"
    default:
      return os.arch()
  }
}

function normalizedOS(): string {
  switch (os.platform()) {
    case "win32":
      return "windows"
    default:
      return os.platform()
  }
}
