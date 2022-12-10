import { ConnectOpts, EngineConn } from "../engineconn.js"
import readline from "readline"
import { execaCommand, ExecaChildProcess } from "execa"
import Client from "../../api/client.gen.js"
import { ConnectParams } from "../../connect.js"
import { EngineSessionConnectParamsParseError } from "../../common/errors/index.js"

/**
 * Bin runs an engine session from a specified binary
 */
export class Bin implements EngineConn {
  private subProcess?: ExecaChildProcess

  private path: string

  constructor(u: URL) {
    this.path = u.host + u.pathname
    if (this.path == "") {
      // this results in execa looking for it in the $PATH
      this.path = "dagger"
    }
  }

  Addr(): string {
    return "http://dagger"
  }

  async Connect(opts: ConnectOpts): Promise<Client> {
    return this.runEngineSession(this.path, opts)
  }

  /**
   * runEngineSession execute the engine binary and set up a GraphQL client that
   * target this engine.
   * TODO:(sipsma) dedupe this with equivalent code in image.ts
   */
  private async runEngineSession(
    engineSessionBinPath: string,
    opts: ConnectOpts
  ): Promise<Client> {
    const engineSessionArgs = [engineSessionBinPath, "session"]

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
