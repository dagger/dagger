// WIP(TomChv): This file shall be renamed to something else
import { Client } from "./client.js";
import { getProvisioner, DEFAULT_HOST } from './provisioning/index.js';

/**
 * ConnectOpts defines option used to run cloak
 * in dev mode.
 * Options are based on `dagger cloak` CLI.
 */
export interface ConnectOpts {
  Workdir?: string;
  ConfigPath?: string;
}

/**
 * connect runs cloak GraphQL server and initializes a
 * GraphQL client to execute query on it through its callback.
 * This implementation is based on the existing Go SDK.
 */
export async function connect(config: ConnectOpts = {}): Promise<Client> {
  // Create config with default values that may be overridden
  // by config if values are set.
  const _config: Required<ConnectOpts> = {
    Workdir: process.env["DAGGER_WORKDIR"] || process.cwd(),
    ConfigPath: process.env["DAGGER_CONFIG"] || "./dagger.json",
    ...config,
  };

  // set host to be DAGGER_HOST env otherwise to provisioning defaults
  const host = process.env["DAGGER_HOST"] || DEFAULT_HOST;
  const gqlClient = await getProvisioner(host).Connect(_config);
  return new Client(gqlClient);
}
