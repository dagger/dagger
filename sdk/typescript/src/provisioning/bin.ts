import AdmZip from "adm-zip"
import * as crypto from "crypto"
import envPaths from "env-paths"
import { execa, ResultPromise } from "execa"
import * as fs from "fs"
import { GraphQLClient } from "graphql-request"
import fetch from "node-fetch"
import * as os from "os"
import * as path from "path"
import * as process from "process"
import readline from "readline"
import * as tar from "tar"
import { fileURLToPath } from "url"

import {
  EngineSessionConnectionTimeoutError,
  EngineSessionConnectParamsParseError,
  EngineSessionError,
  InitEngineSessionBinaryError,
} from "../common/errors/index.js"
import { createGQLClient } from "../common/graphql/client.js"
import { ConnectOpts, EngineConn, ConnectParams } from "./engineconn.js"

const CLI_HOST = "dl.dagger.io"
let OVERRIDE_CLI_URL: string
let OVERRIDE_CHECKSUMS_URL: string

export type ExecaChildProcess = ResultPromise<{
  stdio: "pipe"
  reject: true
  cleanup: true
}>

/**
 * Bin runs an engine session from a specified binary
 */
export class Bin implements EngineConn {
  private _subProcess?: ExecaChildProcess

  private binPath?: string
  private cliVersion?: string

  private readonly cacheDir = path.join(
    `${process.env.XDG_CACHE_HOME?.trim() || envPaths("", { suffix: "" }).cache}`,
    "dagger",
  )

  private readonly DAGGER_CLI_BIN_PREFIX = "dagger"

  constructor(binPath?: string, cliVersion?: string) {
    this.binPath = binPath
    this.cliVersion = cliVersion
  }

  Addr(): string {
    return "http://dagger"
  }

  get subProcess(): ExecaChildProcess | undefined {
    return this._subProcess
  }

  async Connect(opts: ConnectOpts): Promise<GraphQLClient> {
    if (!this.binPath) {
      if (opts.LogOutput) {
        opts.LogOutput.write("Downloading CLI... ")
      }

      this.binPath = await this.downloadCLI()

      if (opts.LogOutput) {
        opts.LogOutput.write("OK!\n")
      }
    }

    return this.runEngineSession(this.binPath, opts)
  }

  private async downloadCLI(): Promise<string> {
    if (!this.cliVersion) {
      throw new Error("cliVersion is not set")
    }

    const binPath = this.buildBinPath()

    // Create a temporary bin file path
    this.createCacheDir()
    const tmpBinDownloadDir = fs.mkdtempSync(
      path.join(this.cacheDir, `temp-${this.getRandomId()}`),
    )
    const tmpBinPath = this.buildOsExePath(
      tmpBinDownloadDir,
      this.DAGGER_CLI_BIN_PREFIX,
    )

    try {
      // download an archive and use appropriate extraction depending on platforms (zip on windows, tar.gz on other platforms)
      const actualChecksum: string = await this.extractArchive(
        tmpBinDownloadDir,
        this.normalizedOS(),
      )
      const expectedChecksum = await this.expectedChecksum()
      if (actualChecksum !== expectedChecksum) {
        throw new Error(
          `checksum mismatch: expected ${expectedChecksum}, got ${actualChecksum}`,
        )
      }
      fs.chmodSync(tmpBinPath, 0o700)
      fs.renameSync(tmpBinPath, binPath)
      fs.rmSync(tmpBinDownloadDir, { recursive: true })
    } catch (e) {
      fs.rmSync(tmpBinDownloadDir, { recursive: true })
      throw new InitEngineSessionBinaryError(
        `failed to download dagger cli binary: ${e}`,
        {
          cause: e as Error,
        },
      )
    }

    // Remove all temporary binary files
    // Ignore current dagger cli or other files that have not be
    // created by this SDK.
    try {
      const files = fs.readdirSync(this.cacheDir)
      files.forEach((file) => {
        const filePath = path.join(this.cacheDir, file)
        if (
          filePath === binPath ||
          !file.startsWith(this.DAGGER_CLI_BIN_PREFIX)
        ) {
          return
        }

        fs.unlinkSync(filePath)
      })
    } catch {
      // Log the error but do not interrupt program.
      console.error("could not clean up temporary binary files")
    }

    return binPath
  }

  /**
   * Traverse up the directory tree to find the package.json file and return the
   * SDK version.
   * @returns the SDK version or "n/a" if the version cannot be found.
   */
  private getSDKVersion() {
    const currentFileUrl = import.meta.url
    const currentFilePath = fileURLToPath(currentFileUrl)
    let currentPath = path.dirname(currentFilePath)

    while (currentPath !== path.parse(currentPath).root) {
      const packageJsonPath = path.join(currentPath, "package.json")
      if (fs.existsSync(packageJsonPath)) {
        try {
          const packageJsonContent = fs.readFileSync(packageJsonPath, "utf8")
          const packageJson = JSON.parse(packageJsonContent)
          return packageJson.version
        } catch {
          return "n/a"
        }
      } else {
        currentPath = path.join(currentPath, "..")
      }
    }
  }

  /**
   * runEngineSession execute the engine binary and set up a GraphQL client that
   * target this engine.
   */
  private async runEngineSession(
    binPath: string,
    opts: ConnectOpts,
  ): Promise<GraphQLClient> {
    const args = ["session"]

    const sdkVersion = this.getSDKVersion()

    const flagsAndValues = [
      { flag: "--workdir", value: opts.Workdir },
      { flag: "--project", value: opts.Project },
      { flag: "--label", value: "dagger.io/sdk.name:nodejs" },
      { flag: "--label", value: `dagger.io/sdk.version:${sdkVersion}` },
    ]

    flagsAndValues.forEach((pair) => {
      if (pair.value) {
        args.push(pair.flag, pair.value)
      }
    })

    if (opts.LogOutput) {
      opts.LogOutput.write("Creating new Engine session... ")
    }

    this._subProcess = execa(binPath, args, {
      stdio: "pipe",
      reject: true,

      // Kill the process if parent exit.
      cleanup: true,

      // Set a long timeout to give time for any cache exports to pack layers up
      // which currently has to happen synchronously with the session.
      forceKillAfterDelay: 300000,
    })

    // Log the output if the user wants to.
    if (opts.LogOutput) {
      this._subProcess.stderr?.pipe(opts.LogOutput)
    }

    const stdoutReader = readline.createInterface({
      input: this._subProcess?.stdout as NodeJS.ReadableStream,
    })

    const timeOutDuration = 300000

    if (opts.LogOutput) {
      opts.LogOutput.write("OK!\nEstablishing connection to Engine... ")
    }

    const connectParams: ConnectParams = (await Promise.race([
      this.readConnectParams(stdoutReader),
      new Promise((_, reject) => {
        setTimeout(() => {
          reject(
            new EngineSessionConnectionTimeoutError(
              "Engine connection timeout",
              {
                timeOutDuration,
              },
            ),
          )
        }, timeOutDuration).unref() // long timeout to account for extensions, though that should be optimized in future
      }),
    ])) as ConnectParams

    if (opts.LogOutput) {
      opts.LogOutput.write("OK!\n")
    }

    return createGQLClient(connectParams.port, connectParams.session_token)
  }

  private async readConnectParams(
    stdoutReader: readline.Interface,
  ): Promise<ConnectParams | undefined> {
    for await (const line of stdoutReader) {
      // parse the line as json-encoded connect params
      const connectParams = JSON.parse(line) as ConnectParams
      if (connectParams.port && connectParams.session_token) {
        return connectParams
      }
      throw new EngineSessionConnectParamsParseError(
        `invalid connect params: ${line}`,
        {
          parsedLine: line,
        },
      )
    }

    // Need to find a better way to handle this part
    // At this stage something wrong happened, `for await` didn't return anything
    // await the subprocess to catch the error
    try {
      await this.subProcess
    } catch {
      this.subProcess?.catch((e) => {
        throw new EngineSessionError(e.stderr)
      })
    }
  }

  async Close(): Promise<void> {
    if (this.subProcess?.pid) {
      this.subProcess.kill("SIGTERM")
    }
  }

  /**
   * createCacheDir will create a cache directory on user
   * host to store dagger binary.
   *
   * If set, it will use envPaths to determine system's cache directory,
   * if not, it will use `$HOME/.cache` as base path.
   * Nothing happens if the directory already exists.
   */
  private createCacheDir(): void {
    fs.mkdirSync(this.cacheDir, { mode: 0o700, recursive: true })
  }

  /**
   * buildBinPath create a path to output dagger cli binary.
   *
   * It will store it in the cache directory with a name composed
   * of the base engine session as constant and the engine identifier.
   */
  private buildBinPath(): string {
    return this.buildOsExePath(
      this.cacheDir,
      `${this.DAGGER_CLI_BIN_PREFIX}-${this.cliVersion}`,
    )
  }

  /**
   * buildExePath create a path to output dagger cli binary.
   */
  private buildOsExePath(destinationDir: string, filename: string): string {
    const binPath = path.join(destinationDir, filename)

    switch (this.normalizedOS()) {
      case "windows":
        return `${binPath}.exe`
      default:
        return binPath
    }
  }

  /**
   * normalizedArch returns the architecture name used by the rest of our SDKs.
   */
  private normalizedArch(): string {
    switch (os.arch()) {
      case "x64":
        return "amd64"
      default:
        return os.arch()
    }
  }

  /**
   * normalizedOS returns the os name used by the rest of our SDKs.
   */
  private normalizedOS(): string {
    switch (os.platform()) {
      case "win32":
        return "windows"
      default:
        return os.platform()
    }
  }

  private cliArchiveName(): string {
    if (OVERRIDE_CLI_URL) {
      return path.basename(new URL(OVERRIDE_CLI_URL).pathname)
    }
    let ext = "tar.gz"
    if (this.normalizedOS() === "windows") {
      ext = "zip"
    }
    return `dagger_v${this.cliVersion}_${this.normalizedOS()}_${this.normalizedArch()}.${ext}`
  }

  private cliArchiveURL(): string {
    if (OVERRIDE_CLI_URL) {
      return OVERRIDE_CLI_URL
    }
    return `https://${CLI_HOST}/dagger/releases/${this.cliVersion}/${this.cliArchiveName()}`
  }

  private cliChecksumURL(): string {
    if (OVERRIDE_CHECKSUMS_URL) {
      return OVERRIDE_CHECKSUMS_URL
    }
    return `https://${CLI_HOST}/dagger/releases/${this.cliVersion}/checksums.txt`
  }

  private async checksumMap(): Promise<Map<string, string>> {
    // download checksums.txt
    const checksums = await fetch(this.cliChecksumURL())
    if (!checksums.ok) {
      throw new Error(
        `failed to download checksums.txt from ${this.cliChecksumURL()}`,
      )
    }
    const checksumsText = await checksums.text()
    // iterate over lines filling in map of filename -> checksum
    const checksumMap = new Map<string, string>()
    for (const line of checksumsText.split("\n")) {
      const [checksum, filename] = line.split(/\s+/)
      checksumMap.set(filename, checksum)
    }
    return checksumMap
  }

  private async expectedChecksum(): Promise<string> {
    const checksumMap = await this.checksumMap()
    const expectedChecksum = checksumMap.get(this.cliArchiveName())
    if (!expectedChecksum) {
      throw new Error(
        `failed to find checksum for ${this.cliArchiveName()} in checksums.txt`,
      )
    }
    return expectedChecksum
  }

  private async extractArchive(destDir: string, os: string): Promise<string> {
    // extract the dagger binary in the cli archive and return the archive of the .zip for windows and .tar.gz for other plateforms
    const archiveResp = await fetch(this.cliArchiveURL())
    if (!archiveResp.ok) {
      throw new Error(
        `failed to download dagger cli archive from ${this.cliArchiveURL()}`,
      )
    }
    if (!archiveResp.body) {
      throw new Error("archive response body is null")
    }

    // create a temporary file to store the archive
    const archivePath = path.join(
      destDir,
      os === "windows" ? "dagger.zip" : "dagger.tar.gz",
    )
    const archiveFile = fs.createWriteStream(archivePath)
    await new Promise((resolve, reject) => {
      archiveResp.body?.pipe(archiveFile)
      archiveResp.body?.on("error", reject)
      archiveFile.on("finish", () => resolve(undefined))
    })

    const actualChecksum = crypto
      .createHash("sha256")
      .update(fs.readFileSync(archivePath))
      .digest("hex")

    if (os === "windows") {
      const zip = new AdmZip(archivePath)
      // extract just dagger.exe to the destdir
      zip.extractEntryTo("dagger.exe", destDir, false, true)
    } else {
      tar.extract({
        cwd: destDir,
        file: archivePath,
        sync: true,
      })
    }

    return actualChecksum
  }

  /**
   * Generate a unix timestamp in nanosecond
   */
  private getRandomId(): string {
    return process.hrtime.bigint().toString()
  }
}

// Only meant for tests
export function _overrideCLIURL(url: string): void {
  OVERRIDE_CLI_URL = url
}

// Only meant for tests
export function _overrideCLIChecksumsURL(url: string): void {
  OVERRIDE_CHECKSUMS_URL = url
}
