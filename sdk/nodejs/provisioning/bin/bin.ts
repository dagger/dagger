import { ConnectOpts, EngineConn } from "../engineconn.js"
import readline from "readline"
import { execaCommand, ExecaChildProcess } from "execa"
import Client from "../../api/client.gen.js"

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
   * TODO:(sipsma) dedupe this with equivalent code in image.ts
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
    })

    const stdoutReader = readline.createInterface({
      // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
      input: this.subProcess.stdout!,
    })

    const sock = (await Promise.race([
      this.readSock(stdoutReader),
      new Promise((_, reject) => {
        setTimeout(() => {
          reject(new Error("timeout reading socket path from engine session"))
        }, 300000).unref() // long timeout to account for extensions, though that should be optimized in future
      }),
    ])) as string

    return new Client({ host: sock })
  }

  private async readSock(stdoutReader: readline.Interface): Promise<string> {
    for await (const line of stdoutReader) {
      // Read line as a sock path
      return line.trim()
    }
    throw new Error("failed to read port from engine session")
  }

  async Close(): Promise<void> {
    if (this.subProcess?.pid) {
      this.subProcess.kill("SIGTERM", {
        forceKillAfterTimeout: 2000,
      })
    }
  }
}
