import Client from './api/client.gen.js';
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

type CallbackFct = (client: Client) => Promise<void>

/**
 * connect runs cloak GraphQL server and initializes a
 * GraphQL client to execute query on it through its callback.
 * This implementation is based on the existing Go SDK.
 */
export async function connect(cb: CallbackFct, config: ConnectOpts = {}): Promise<void> {
	// Create config with default values that may be overridden
	// by config if values are set.
	const _config: Required<ConnectOpts> = {
		Workdir: process.env['DAGGER_WORKDIR'] || process.cwd(),
		ConfigPath: process.env['DAGGER_CONFIG'] || './dagger.json',
		...config,
	};

	// set host to be DAGGER_HOST env otherwise to provisioning defaults
	const host = process.env['DAGGER_HOST'] || DEFAULT_HOST;
	const provisioner = getProvisioner(host);

	await cb(await provisioner.Connect(_config)).finally(async () => provisioner.Close());
}
