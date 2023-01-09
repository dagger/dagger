import AdmZip from "adm-zip"
import * as crypto from "crypto"
import envPaths from "env-paths"
import { execaCommand, ExecaChildProcess } from "execa"
import * as fs from "fs"
import fetch from "node-fetch"
import * as os from "os"
import * as path from "path"
import readline from "readline"
import * as tar from "tar"

import Client from "../api/client.gen.js"
import {
  EngineSessionConnectionTimeoutError,
  EngineSessionConnectParamsParseError,
  EngineSessionEOFError,
  InitEngineSessionBinaryError,
} from "../common/errors/index.js"
import { ConnectParams } from "../connect.js"
import { ConnectOpts, EngineConn } from "./engineconn.js"

const CLI_HOST = "dl.dagger.io"
let OVERRIDE_CLI_URL: string
let OVERRIDE_CHECKSUMS_URL: string

/**
 * Bin runs an engine session from a specified binary
 */
export class Bin implements EngineConn {
  private subProcess?: ExecaChildProcess

  private binPath?: string
  private cliVersion?: string

  private readonly cacheDir = envPaths("dagger", { suffix: "" }).cache

  private readonly DAGGER_CLI_BIN_PREFIX = "dagger"

  constructor(binPath?: string, cliVersion?: string) {
    this.binPath = binPath
    this.cliVersion = cliVersion
  }

  Addr(): string {
    return "http://dagger"
  }

  async Connect(opts: ConnectOpts): Promise<Client> {
    if (!this.binPath) {
      this.binPath = await this.downloadCLI()
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
      path.join(this.cacheDir, `temp-${this.getRandomId()}`)
    )
    const tmpBinPath = path.join(tmpBinDownloadDir, "dagger")

    try {
      // download an archive and use appropriate extraction depending on platforms (zip on windows, tar.gz on other platforms)
      const actualChecksum: string = await this.extractArchive(
        tmpBinDownloadDir,
        this.normalizedOS()
      )
      const expectedChecksum = await this.expectedChecksum()
      if (actualChecksum !== expectedChecksum) {
        throw new Error(
          `checksum mismatch: expected ${expectedChecksum}, got ${actualChecksum}`
        )
      }
      fs.chmodSync(tmpBinPath, 0o700)
      fs.renameSync(tmpBinPath, binPath)
      fs.rmSync(tmpBinDownloadDir, { recursive: true })
    } catch (e) {
      fs.rmSync(tmpBinDownloadDir, { recursive: true })
      throw new InitEngineSessionBinaryError(
        `failed to download dagger cli binary: ${e}`,
        { cause: e as Error }
      )
    }

    // Remove all temporary binary files
    // Ignore current dagger cli or other files that have not be
    // created by this SDK.
    try {
      const files = fs.readdirSync(this.cacheDir)
      files.forEach((file) => {
        const filePath = `${this.cacheDir}/${file}`
        if (
          filePath === binPath ||
          !file.startsWith(this.DAGGER_CLI_BIN_PREFIX)
        ) {
          return
        }

        fs.unlinkSync(filePath)
      })
    } catch (e) {
      // Log the error but do not interrupt program.
      console.error("could not clean up temporary binary files")
    }

    return binPath
  }

  /**
   * runEngineSession execute the engine binary and set up a GraphQL client that
   * target this engine.
   */
  private async runEngineSession(
    binPath: string,
    opts: ConnectOpts
  ): Promise<Client> {
    const args = [binPath, "session"]

    if (opts.Workdir) {
      args.push("--workdir", opts.Workdir)
    }
    if (opts.Project) {
      args.push("--project", opts.Project)
    }

    this.subProcess = execaCommand(args.join(" "), {
      stderr: opts.LogOutput || "ignore",
      reject: true,

      // Kill the process if parent exit.
      cleanup: true,
    })

    const stdoutReader = readline.createInterface({
      // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
      input: this.subProcess.stdout!,
    })

    const timeOutDuration = 300000

    const connectParams: ConnectParams = (await Promise.race([
      this.readConnectParams(stdoutReader),
      new Promise((_, reject) => {
        setTimeout(() => {
          reject(
            new EngineSessionConnectionTimeoutError(
              "timeout reading connect params from engine session",
              { timeOutDuration }
            )
          )
        }, timeOutDuration).unref() // long timeout to account for extensions, though that should be optimized in future
      }),
    ])) as ConnectParams

    return new Client({
      host: `127.0.0.1:${connectParams.port}`,
      sessionToken: connectParams.session_token,
    })
  }

  private async readConnectParams(
    stdoutReader: readline.Interface
  ): Promise<ConnectParams> {
    for await (const line of stdoutReader) {
      // parse the the line as json-encoded connect params
      const connectParams = JSON.parse(line) as ConnectParams
      if (connectParams.port && connectParams.session_token) {
        return connectParams
      }
      throw new EngineSessionConnectParamsParseError(
        `invalid connect params: ${line}`,
        { parsedLine: line }
      )
    }
    throw new EngineSessionEOFError(
      "No line was found to parse the engine connect params"
    )
  }

  async Close(): Promise<void> {
    if (this.subProcess?.pid) {
      this.subProcess.kill("SIGTERM", {
        forceKillAfterTimeout: 2000,
      })
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
    const binPath = `${this.cacheDir}/${this.DAGGER_CLI_BIN_PREFIX}-${this.cliVersion}`

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
    return `dagger_v${
      this.cliVersion
    }_${this.normalizedOS()}_${this.normalizedArch()}.${ext}`
  }

  private cliArchiveURL(): string {
    if (OVERRIDE_CLI_URL) {
      return OVERRIDE_CLI_URL
    }
    return `https://${CLI_HOST}/dagger/releases/${
      this.cliVersion
    }/${this.cliArchiveName()}`
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
        `failed to download checksums.txt from ${this.cliChecksumURL()}`
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
        `failed to find checksum for ${this.cliArchiveName()} in checksums.txt`
      )
    }
    return expectedChecksum
  }

  private async extractArchive(destDir: string, os: string): Promise<string> {
    // extract the dagger binary in the cli archive and return the archive of the .zip for windows and .tar.gz for other plateforms
    const archiveResp = await fetch(this.cliArchiveURL())
    if (!archiveResp.ok) {
      throw new Error(
        `failed to download dagger cli archive from ${this.cliArchiveURL()}`
      )
    }
    if (!archiveResp.body) {
      throw new Error("archive response body is null")
    }

    // create a temporary file to store the archive
    const archivePath = `${destDir}/${
      os === "windows" ? "dagger.zip" : "dagger.tar.gz"
    }`
    const archiveFile = fs.createWriteStream(archivePath)
    await new Promise((resolve, reject) => {
      archiveResp.body?.pipe(archiveFile)
      archiveResp.body?.on("error", reject)
      archiveFile.on("finish", resolve)
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
