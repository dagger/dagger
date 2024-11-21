import { GraphQLClient } from "graphql-request"

import { createGQLClient } from "../../common/graphql/client.js"
import { ConnectOpts } from "../../connectOpts.js"
import { EngineConn } from "../engineconn.js"

export class ModuleProvisioning implements EngineConn {
  public async Connect(config: ConnectOpts): Promise<GraphQLClient> {
    if (
      !process.env["DAGGER_SESSION_PORT"] &&
      !process.env["DAGGER_SESSION_TOKEN"]
    ) {
      throw new Error(
        "DAGGER_SESSION_PORT and DAGGER_SESSION_TOKEN must be set when using dagger with modules",
      )
    }
    const daggerSessionPort = process.env["DAGGER_SESSION_PORT"]
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

  public async Close(): Promise<void> {
    // nothing to do, it's handled by the runtime
  }
}
