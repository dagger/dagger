// WIP(TomChv): This file shall be renamed to something else
import { execa, execaCommandSync } from 'execa';
import axios from 'axios';
import Client from './api/client.js';

/**
 * Cloak binary name
 */
const CLOAK_BINARY = "cloak";

/**
 * ConnectOpts defines option used to run cloak
 * in dev mode.
 * Options are based on `dagger cloak` CLI.
 */
export interface ConnectOpts {
	Port?: number;
	Workdir?: string;
	ConfigPath?: string;
}

/**
 * ConnectExecCB is the type of the connect callback
 * This call acts as a context with a ready to use Dagger GraphQL client.
 */
export type ConnectExecCB = (client: Client) => Promise<void>;

/**
 * connect runs cloak GraphQL server and initializes a
 * GraphQL client to execute query on it through its callback.
 * This implementation is based on the existing Go SDK.
 */
export async function connect(cb: ConnectExecCB, config: ConnectOpts = {}): Promise<void> {
	// exit with error if we are not using the non-Cloak dagger binary (< 0.3.0)
	await verifyCloakBinary();

	// Create config with default values that may be overridden
	// by config if values are set.
	const _config: Required<ConnectOpts> = {
		Workdir: process.env['DAGGER_WORKDIR'] || process.cwd(),
		ConfigPath: process.env['DAGGER_CONFIG'] || './dagger.json',
		Port: 8080,
		...config
	};

	const args = buildCLIArguments(_config);

	// Start Cloak server.
	const serverProcess = execa(CLOAK_BINARY, args, {
		stdio: "inherit",
		cwd: _config.Workdir
	});

	// Wait for Cloak server to be ready.
	await waitCloakServer(_config.Port)

	// Execute users workflow and shutdown the server at the end of the
	// execution. Either it succeeds or fails.
	await cb(new Client({port: _config.Port}))
		.finally(async () => {
			serverProcess.cancel();
			await serverProcess.catch((e) => {
				if (!e.isCanceled) {
					console.error('dagger engine error: ', e);
				}
			});
		});
}

/**
 * verifyCloakBinary make sure that user is using the correct version of
 * Dagger (> 0.3.0).
 * This function exits on error.
 */
async function verifyCloakBinary() {
	try {
		execaCommandSync('cloak dev --help');
	} catch (err) {
		console.error('⚠️  Please ensure that cloak binary in $PATH is v0.3.0 or newer.');
		// https://tldp.org/LDP/abs/html/exitcodes.html
		// | 127 | "command not found" | illegal_command | Possible problem with $PATH or a typo |
		process.exit(127);
	}
}

/**
 * buildCLIArguments creates a list of string that correspond to
 * the arguments concatenated to "cloak dev" command.
 */
function buildCLIArguments(opts: Required<ConnectOpts>): string[] {
	return [
		'dev',
		'--workdir', `${ opts.Workdir }`,
		'-p', `${ opts.ConfigPath }`,
		'--port', `${ opts.Port }`,
	];
}

/**
 * waitCloakServer use an axios client to try connecting to the cloak server
 * until it successfully connect.
 * This function has a timeout of 3 minutes to imports and installs all
 * extensions.
 */
async function waitCloakServer(port: number) {
	const client = axios.create({
		baseURL: `http://localhost:${port}`,
	});

	// Wait 500ms between each attempt.
	for (let i = 0; i < 360; i++) {
		try {
			await client.get("/query");
		} catch (e) {
			await new Promise((resolve) => setTimeout(resolve, 500));
		}
	}
}