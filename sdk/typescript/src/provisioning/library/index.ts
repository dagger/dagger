import { GraphQLClient } from "graphql-request"

import { createGQLClient } from "../../common/graphql/client.js"
import { ConnectOpts } from "../../connectOpts.js"
import { CLI_VERSION } from "../default.js"
import { EngineConn } from "../engineconn.js"
import { Bin, ExecaChildProcess } from "./bin.js"

export class LibraryProvisioning implements EngineConn {
  private _subProcess?: ExecaChildProcess

  public async Connect(config: ConnectOpts): Promise<GraphQLClient> {
    const daggerSessionPort = process.env["DAGGER_SESSION_PORT"]
    if (daggerSessionPort) {
      const sessionToken = process.env["DAGGER_SESSION_TOKEN"]
      if (!sessionToken) {
        throw new Error(
          "DAGGER_SESSION_TOKEN must be set when using DAGGER_SESSION_PORT",
        )
      }

      if (config.Workdir && config.Workdir !== "") {
        throw new Error(
          "cannot configure workdir for existing session (please use --workdir or host.directory with absolute paths instead)",
        )
      }

      return createGQLClient(Number(daggerSessionPort), sessionToken)
    }

    const cliBin = process.env["_EXPERIMENTAL_DAGGER_CLI_BIN"]
    const engineConn = new Bin(cliBin, CLI_VERSION)

    const client = await engineConn.Connect(config)

    this._subProcess = engineConn.subProcess

    return client
  }

  public Close(): void {
    if (this._subProcess) {
      this._subProcess.kill("SIGTERM")
    }
  }
}
