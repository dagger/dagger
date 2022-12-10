import { ConnectOpts, EngineConn } from "../engineconn.js"
import * as path from "path"
import * as fs from "fs"
import * as os from "os"
import readline from "readline"
import { execaCommandSync, execaCommand, ExecaChildProcess } from "execa"
import Client from "../../api/client.gen.js"
import { ConnectParams } from "../../connect.js"
import {
  DockerImageRefValidationError,
  EngineSessionConnectParamsParseError,
  InitEngineSessionBinaryError,
} from "../../common/errors/index.js"

/**
 * ImageRef is a simple abstraction of docker image reference.
 */
class ImageRef {
  private readonly ref: string

  /**
   * id is the unique identifier of the image
   * based on image's digest.
   */
  private readonly id: string

  /**
   * trim image digests to 16 characters to make output more readable.
   */
  private readonly DIGEST_LEN = 16

  constructor(ref: string) {
    // Throw error if ref is not correctly formatted.
    ImageRef.validate(ref)

    this.ref = ref

    const id = ref.split("@sha256:", 2)[1]
    this.id = id.slice(0, this.DIGEST_LEN)
  }

  get Ref(): string {
    return this.ref
  }

  get ID(): string {
    return this.id
  }

  /**
   * validateImage verify that the passed ref
   * is compliant with DockerImage constructor.
   *
   * This function does not return anything but
   * only throw on error.
   *
   * @throws no digest found in ref.
   */
  static validate(ref: string): void {
    if (!ref.includes("@sha256:")) {
      throw new DockerImageRefValidationError(`no digest found in ref ${ref}`, {
        ref: ref,
      })
    }
  }
}

/**
 * DockerImage is an implementation of EngineConn to set up a Dagger
 * Engine session from a pulled docker image.
 */
export class DockerImage implements EngineConn {
  private imageRef: ImageRef

  private readonly cacheDir = path.join(
    process.env.XDG_CACHE_HOME || path.join(os.homedir(), ".cache"),
    "dagger"
  )

  private readonly DAGGER_CLI_BIN_PREFIX = "dagger"

  private subProcess?: ExecaChildProcess

  constructor(u: URL) {
    this.imageRef = new ImageRef(u.host + u.pathname)
  }

  /**
   * Generate a unix timestamp in nanosecond
   */
  private getRandomId(): string {
    return process.hrtime.bigint().toString()
  }

  Addr(): string {
    return "http://dagger"
  }

  async Connect(opts: ConnectOpts): Promise<Client> {
    this.createCacheDir()

    const engineSessionBinPath = this.buildBinPath()
    if (!fs.existsSync(engineSessionBinPath)) {
      this.pullEngineSessionBin(engineSessionBinPath)
    }

    return this.runEngineSession(engineSessionBinPath, opts)
  }

  /**
   * createCacheDir will create a cache directory on user
   * host to store dagger binary.
   *
   * If set, it will use XDG directory, if not, it will use `$HOME/.cache`
   * as base path.
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
    const binPath = `${this.cacheDir}/${this.DAGGER_CLI_BIN_PREFIX}-${this.imageRef.ID}`

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

  /**
   * pullEngineSessionBin will retrieve Dagger binary from its docker image
   * and copy it to the local host.
   * This function automatically resolves host's platform to copy the correct
   * binary.
   */
  private pullEngineSessionBin(engineSessionBinPath: string): void {
    // Create a temporary bin file path
    const tmpBinPath = path.join(
      this.cacheDir,
      `temp-${this.DAGGER_CLI_BIN_PREFIX}-${this.getRandomId()}`
    )

    const dockerRunArgs = [
      "docker",
      "run",
      "--rm",
      "--entrypoint",
      "/bin/cat",
      this.imageRef.Ref,
      `/usr/bin/${
        this.DAGGER_CLI_BIN_PREFIX
      }-${this.normalizedOS()}-${this.normalizedArch()}`,
    ]

    try {
      const fd = fs.openSync(tmpBinPath, "w", 0o700)

      execaCommandSync(dockerRunArgs.join(" "), {
        stdout: fd,
        stderr: "pipe",
        encoding: null,
        // Kill the process if parent exit.
        cleanup: true,
        // Throw on error
        reject: true,
        timeout: 300000,
      })

      fs.closeSync(fd)
      fs.renameSync(tmpBinPath, engineSessionBinPath)
    } catch (e) {
      fs.rmSync(tmpBinPath)
      throw new InitEngineSessionBinaryError(
        `failed to copy dagger cli binary: ${e}`,
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
          filePath === engineSessionBinPath ||
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
  }

  /**
   * runEngineSession execute the engine binary and set up a GraphQL client that
   * target this engine.
   */
  private async runEngineSession(
    engineSessionBinPath: string,
    opts: ConnectOpts
  ): Promise<Client> {
    const env = process.env
    if (!env._EXPERIMENTAL_DAGGER_RUNNER_HOST) {
      env._EXPERIMENTAL_DAGGER_RUNNER_HOST = `docker-image://${this.imageRef.Ref}`
    }

    const engineSessionArgs = [engineSessionBinPath]

    if (opts.Workdir) {
      engineSessionArgs.push("--workdir", opts.Workdir)
    }
    if (opts.Project) {
      engineSessionArgs.push("--project", opts.Project)
    }

    this.subProcess = execaCommand(engineSessionArgs.join(" "), {
      stderr: opts.LogOutput || "ignore",

      // Kill the process if parent exit.
      cleanup: true,

      env: env,
    })

    const stdoutReader = readline.createInterface({
      // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
      input: this.subProcess.stdout!,
    })

    const connectParams: ConnectParams = (await Promise.race([
      this.readConnectParams(stdoutReader),
      new Promise((_, reject) => {
        setTimeout(() => {
          reject(
            new EngineSessionConnectParamsParseError(
              "timeout reading connect params from engine session"
            )
          )
        }, 300000).unref() // long timeout to account for extensions, though that should be optimized in future
      }),
    ])) as ConnectParams

    return new Client({
      host: connectParams.host,
      sessionToken: connectParams.session_token,
    })
  }

  private async readConnectParams(
    stdoutReader: readline.Interface
  ): Promise<ConnectParams> {
    for await (const line of stdoutReader) {
      // parse the the line as json-encoded connect params
      const connectParams = JSON.parse(line) as ConnectParams
      if (connectParams.host && connectParams.session_token) {
        return connectParams
      }
      throw new EngineSessionConnectParamsParseError(
        `invalid connect params: ${line}`
      )
    }
    throw new EngineSessionConnectParamsParseError(
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
}
