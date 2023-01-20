import AdmZip from "adm-zip"
import * as crypto from "crypto"
import envPaths from "env-paths"
import * as fs from "fs"
import fetch from "node-fetch"
import * as os from "os"
import * as path from "path"
import * as tar from "tar"

import { InitEngineSessionBinaryError } from "../common/errors/index.js"

const CLI_HOST = "dl.dagger.io"
const DAGGER_CLI_BIN_PREFIX = "dagger"
const CACHE_DIR = path.join(
  `${process.env.XDG_CACHE_HOME?.trim() || envPaths("", { suffix: "" }).cache}`,
  "dagger"
)

interface CliDownloaderOptions {
  cliVersion: string
  archive?: {
    checksumUrl?: string
    url?: string
    name?(architecture: string): string
    extract?(archivePath: string, destinationPath: string): void
    path?(destinationFolder: string): string
  }
  executableFilename?(name: string): string
}

export class CliDownloader {
  private readonly cliVersion: string
  private readonly archive?: CliDownloaderOptions["archive"]
  private readonly executableFilename: (name: string) => string

  constructor(options: CliDownloaderOptions) {
    if (!options.cliVersion) {
      throw new Error("cliVersion is not set")
    }

    this.cliVersion = options.cliVersion
    this.archive = options.archive
    this.executableFilename =
      options.executableFilename ?? ((name: string) => name)
  }

  static async Download(options: CliDownloaderOptions): Promise<string> {
    const cliDownloader =
      os.platform() === "win32"
        ? new WindowsCliDownloader(options)
        : new CliDownloader(options)

    return await cliDownloader.Download()
  }

  async Download(): Promise<string> {
    // Create a temporary bin file path
    this.createCacheDir()

    const binPath = path.join(
      CACHE_DIR,
      this.executableFilename(`${DAGGER_CLI_BIN_PREFIX}-${this.cliVersion}`)
    )

    if (fs.existsSync(binPath)) {
      return binPath
    }

    const tmpBinDownloadDir = fs.mkdtempSync(
      path.join(CACHE_DIR, `temp-${this.getRandomId()}`)
    )

    const tmpBinPath = path.join(
      tmpBinDownloadDir,
      this.executableFilename(DAGGER_CLI_BIN_PREFIX)
    )

    try {
      // download an archive and use appropriate extraction depending on platforms (zip on windows, tar.gz on other platforms)
      const actualChecksum: string = await this.extractArchive(
        tmpBinDownloadDir
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
      const files = fs.readdirSync(CACHE_DIR)
      files.forEach((file) => {
        const filePath = path.join(CACHE_DIR, file)

        if (filePath === binPath || !file.startsWith(DAGGER_CLI_BIN_PREFIX)) {
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

  private archiveURL(): string {
    if (this.archive?.url) {
      return this.archive.url
    }

    return `https://${CLI_HOST}/dagger/releases/${
      this.cliVersion
    }/${this.cliArchiveName()}`
  }

  private checksumURL(): string {
    if (this.archive?.checksumUrl) {
      return this.archive.checksumUrl
    }

    return `https://${CLI_HOST}/dagger/releases/${this.cliVersion}/checksums.txt`
  }

  private archivePath(destinationFolder: string): string {
    if (this.archive?.path != null) {
      return this.archive.path(destinationFolder)
    }

    return path.join(destinationFolder, `${DAGGER_CLI_BIN_PREFIX}.tar.gz`)
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
    fs.mkdirSync(CACHE_DIR, { mode: 0o700, recursive: true })
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

  private cliArchiveName(): string {
    const architecture = this.normalizedArch()

    if (this.archive?.name != null) {
      return this.archive.name(architecture)
    }

    return `dagger_v${this.cliVersion}_${os.platform()}_${architecture}.tar.gz`
  }

  private async checksumMap(): Promise<Map<string, string>> {
    // download checksums.txt
    const checksums = await fetch(this.checksumURL())
    if (!checksums.ok) {
      throw new Error(
        `failed to download checksums.txt from ${this.checksumURL()}`
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

  private async extractArchive(destDir: string): Promise<string> {
    // extract the dagger binary in the cli archive and return the archive of the .zip for windows and .tar.gz for other plateforms
    const archiveResp = await fetch(this.archiveURL())
    if (!archiveResp.ok) {
      throw new Error(
        `failed to download dagger cli archive from ${this.archiveURL()}`
      )
    }
    if (!archiveResp.body) {
      throw new Error("archive response body is null")
    }

    // create a temporary file to store the archive
    const archivePath = this.archivePath(destDir)
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

    if (this.archive?.extract != null) {
      this.archive.extract(archivePath, destDir)
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

export class WindowsCliDownloader extends CliDownloader {
  constructor(
    options: Omit<CliDownloaderOptions, "archive" | "executableFilename">
  ) {
    super({
      ...options,
      executableFilename(name) {
        return `${name}.exe`
      },
      archive: {
        extract(archivePath, destinationPath) {
          const zip = new AdmZip(archivePath)
          zip.extractEntryTo(
            `${DAGGER_CLI_BIN_PREFIX}.exe`,
            destinationPath,
            false,
            true
          )
        },
        name(architecture) {
          return `${DAGGER_CLI_BIN_PREFIX}_v${options.cliVersion}_windows_${architecture}.zip`
        },
        path(destinationFolder) {
          return path.join(destinationFolder, `${DAGGER_CLI_BIN_PREFIX}.zip`)
        },
      },
    })
  }
}
