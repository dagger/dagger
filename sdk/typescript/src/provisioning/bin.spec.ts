import assert from "assert"
import * as fs from "fs"
import * as http from "http"
import { afterEach, beforeEach, describe, it } from "mocha"
import { type AddressInfo } from "net"
import * as os from "os"
import * as path from "path"
import { PassThrough } from "stream"

import { _overrideCLIChecksumsURL, _overrideCLIURL, Bin } from "./bin.js"

describe("CLI provisioning", function () {
  let engineConn: Bin
  let tempDir: string
  let previousCacheHome: string | undefined
  let previousPath: string | undefined
  let server: http.Server | undefined

  beforeEach(() => {
    tempDir = fs.mkdtempSync(path.join(os.tmpdir(), "dagger-test-"))
    previousCacheHome = process.env.XDG_CACHE_HOME
    previousPath = process.env.PATH
    process.env.XDG_CACHE_HOME = path.join(tempDir, "cache")
    engineConn = new Bin(undefined, "unreleased")
  })

  afterEach(async () => {
    _overrideCLIURL("")
    _overrideCLIChecksumsURL("")
    restoreEnv("XDG_CACHE_HOME", previousCacheHome)
    restoreEnv("PATH", previousPath)
    fs.rmSync(tempDir, { recursive: true })

    if (server) {
      await new Promise<void>((resolve, reject) => {
        server?.close((error) => (error ? reject(error) : resolve()))
      })
      server = undefined
    }
  })

  describe("fallbackToLocalCLI", function () {
    it("uses the dagger CLI in PATH", async function () {
      await overrideChecksumsResponse(403)
      const binDir = path.join(tempDir, "bin")
      fs.mkdirSync(binDir)
      const binPath = path.join(
        binDir,
        os.platform() === "win32" ? "dagger.exe" : "dagger",
      )
      fs.writeFileSync(binPath, "", { mode: 0o700 })
      process.env.PATH = binDir

      const downloadError = await getError(engineConn["downloadCLI"]())
      const logs = new PassThrough()

      const actual = engineConn["fallbackToLocalCLI"](downloadError, logs)

      assert.equal(actual, binPath)
      assert.match(logs.read().toString(), /compatibility is not guaranteed/)
    })

    it("returns other download errors", function () {
      const downloadError = new Error("download failed")

      assert.throws(
        () => engineConn["fallbackToLocalCLI"](downloadError),
        (error) => error === downloadError,
      )
    })

    it("preserves download and PATH errors when no CLI is found", async function () {
      await overrideChecksumsResponse(403)
      process.env.PATH = tempDir

      const downloadError = await getError(engineConn["downloadCLI"]())

      assert.throws(
        () => engineConn["fallbackToLocalCLI"](downloadError),
        (error) => {
          assert(error instanceof AggregateError)
          assert.match(error.message, /failed to download dagger cli binary/)
          assert.match(error.message, /dagger executable was not found/)
          return true
        },
      )
    })
  })

  describe("release availability", function () {
    // Missing checksums mean the release is absent; a missing archive may be a
    // partial or broken release, so it must remain fatal.
    for (const status of [403, 404]) {
      it(`marks checksums returning ${status} as unavailable`, async function () {
        await overrideChecksumsResponse(status)

        const error = await getError(engineConn["checksumMap"]())

        assert(engineConn["hasCLIReleaseUnavailableError"](error))
      })
    }

    it("does not mark a missing archive as unavailable", async function () {
      const baseURL = await startServer(404)
      _overrideCLIURL(`${baseURL}/archive.tar.gz`)

      const error = await getError(
        engineConn["extractArchive"](tempDir, engineConn["normalizedOS"]()),
      )

      assert(!engineConn["hasCLIReleaseUnavailableError"](error))
    })
  })

  describe("Windows PATH resolution", function () {
    // Match Go's exec.LookPath and PowerShell: skip empty PATH entries and
    // normalize PATHEXT before searching.
    it("normalizes PATH and PATHEXT", function () {
      const candidates = engineConn["daggerCLIPathCandidates"](
        "windows",
        String.raw`C:\bin;;"D:\other bin"`,
        "EXE;;.CMD;",
      )

      assert.deepEqual(candidates, [
        String.raw`C:\bin\dagger.exe`,
        String.raw`C:\bin\dagger.cmd`,
        String.raw`D:\other bin\dagger.exe`,
        String.raw`D:\other bin\dagger.cmd`,
      ])
    })

    it("uses default executable extensions when PATHEXT is empty", function () {
      const candidates = engineConn["daggerCLIPathCandidates"](
        "windows",
        String.raw`C:\bin`,
        "",
      )

      assert.deepEqual(candidates, [
        String.raw`C:\bin\dagger.com`,
        String.raw`C:\bin\dagger.exe`,
        String.raw`C:\bin\dagger.bat`,
        String.raw`C:\bin\dagger.cmd`,
      ])
    })
  })

  async function overrideChecksumsResponse(status: number): Promise<void> {
    const baseURL = await startServer(status)
    _overrideCLIChecksumsURL(`${baseURL}/checksums.txt`)
  }

  async function startServer(status: number): Promise<string> {
    server = http.createServer((_req, res) => {
      res.writeHead(status)
      res.end()
    })
    await new Promise<void>((resolve) => {
      server?.listen(0, "127.0.0.1", resolve).unref()
    })
    const addr = server.address() as AddressInfo
    return `http://${addr.address}:${addr.port}`
  }
})

async function getError(promise: Promise<unknown>): Promise<Error> {
  try {
    await promise
  } catch (error) {
    assert(error instanceof Error)
    return error
  }
  throw new Error("expected operation to fail")
}

function restoreEnv(name: string, value: string | undefined): void {
  if (value === undefined) {
    delete process.env[name]
  } else {
    process.env[name] = value
  }
}
