import { ConnectOpts } from "../connectOpts.js"
import { createGQLClient } from "../graphql/client.js"
import { Bin, CLI_VERSION } from "../provisioning/index.js"
import { Context } from "./context.js"

/**
 * @hidden
 *
 * Initialize a default client context from environment.
 */
export async function initDefaultContext(
  cfg: ConnectOpts = {},
): Promise<Context> {
  let ctx = new Context()

  // Prefer DAGGER_SESSION_PORT if set
  const daggerSessionPort = process.env["DAGGER_SESSION_PORT"]
  if (daggerSessionPort) {
    const sessionToken = process.env["DAGGER_SESSION_TOKEN"]
    if (!sessionToken) {
      throw new Error(
        "DAGGER_SESSION_TOKEN must be set when using DAGGER_SESSION_PORT",
      )
    }

    if (cfg.Workdir && cfg.Workdir !== "") {
      throw new Error(
        "cannot configure workdir for existing session (please use --workdir or host.directory with absolute paths instead)",
      )
    }

    ctx = new Context({
      client: createGQLClient(Number(daggerSessionPort), sessionToken),
    })
  } else {
    // Otherwise, prefer _EXPERIMENTAL_DAGGER_CLI_BIN, with fallback behavior of
    // downloading the CLI and using that as the bin.
    const cliBin = process.env["_EXPERIMENTAL_DAGGER_CLI_BIN"]
    const engineConn = new Bin(cliBin, CLI_VERSION)
    const client = await engineConn.Connect(cfg)

    ctx = new Context({ client, subProcess: engineConn.subProcess })
  }

  return ctx
}
