import { execaCommand, ExecaChildProcess } from "execa"
import readline from "readline"

import Client from "../api/client.gen.js"
import {
  EngineSessionConnectionTimeoutError,
  EngineSessionConnectParamsParseError,
  EngineSessionEOFError,
} from "../common/errors/index.js"
import { ConnectParams } from "../connect.js"
import { ConnectOpts, EngineConn } from "./engineconn.js"

/**
 * Bin runs an engine session from a specified binary
 */
export class Bin implements EngineConn {
  private subProcess?: ExecaChildProcess

  constructor(private readonly binPath: string) {}

  Addr(): string {
    return "http://dagger"
  }

  async Connect(opts: ConnectOpts): Promise<Client> {
    return this.runEngineSession(this.binPath, opts)
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
}
