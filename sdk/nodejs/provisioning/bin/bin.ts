import { ExecaChildProcess, execaCommand } from "execa"
import readline from "readline"

import Client from "../../api/client.gen.js"
import { EngineSessionPortParseError } from "../../common/errors/index.js"
import { ConnectOpts, EngineConn } from "../engineconn.js"

/**
 * Bin runs an engine session from a specified binary
 */
export class Bin implements EngineConn {
  private subProcess?: ExecaChildProcess

  private readonly path: string

  constructor(u: URL) {
    this.path = u.host + u.pathname
    if (this.path == "") {
      // this results in execa looking for it in the $PATH
      this.path = "dagger-engine-session"
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
   */
  private async runEngineSession(
    engineSessionBinPath: string,
    opts: ConnectOpts
  ): Promise<Client> {
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
      env: process.env,
    })

    const stdoutReader = readline.createInterface({
      // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
      input: this.subProcess.stdout!,
    })

    const port = await Promise.race([
      this.readPort(stdoutReader),
      new Promise((_, reject) => {
        setTimeout(() => {
          reject(
            new EngineSessionPortParseError(
              "timeout reading port from engine session"
            )
          )
        }, 300000).unref() // long timeout to account for extensions, though that should be optimized in future
      }),
    ])

    return new Client({ host: `127.0.0.1:${port}` })
  }

  private async readPort(stdoutReader: readline.Interface): Promise<number> {
    for await (const line of stdoutReader) {
      // Read line as a port number
      const port = parseInt(line)
      if (isNaN(port)) {
        throw new EngineSessionPortParseError(
          `failed to parse port from engine session while parsing: ${line}`,
          { parsedLine: line }
        )
      }
      return port
    }
    throw new EngineSessionPortParseError(
      "No line was found to parse the engine port"
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
