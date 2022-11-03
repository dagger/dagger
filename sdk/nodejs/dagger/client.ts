import axios from 'axios';
import { buildAxiosFetch } from '@lifeomic/axios-fetch';
import { GraphQLClient } from 'graphql-request';

import { Response } from 'node-fetch';
import { ServerProcess } from './connect.js';

// @ts-expect-error node-fetch doesn't exactly match the Response object, but close enough.
global.Response = Response;

export const client = new GraphQLClient('http://fake.invalid/query', {
	fetch: buildAxiosFetch(
		axios.create({
			socketPath: '/dagger.sock',
			timeout: 3600e3,
		})
	),
});

export class Client {
	private client: GraphQLClient;

	/**
	 * hold serverProcess so it can be closed anytime.
	 * This is an optional member because users may use this client
	 * without launching the server from the Typescript SDK.
	 * @private
	 */
	private readonly serverProcess?: ServerProcess;

	/**
	 * creates a new Dagger Typescript SDK GraphQL client.
	 * If the client is created by `dagger.connect()`, it will
	 * hold the serverProcess, so it can be closed using `close()`
	 * method.
	 */
	constructor(port = 8080, serverProcess?: ServerProcess) {
		this.client = new GraphQLClient(`http://localhost:${port}/query`);
		this.serverProcess = serverProcess;
	}

	/**
	 * do takes a GraphQL query payload as parameter and send it
	 * to Cloak server to execute every operation's in it.
	 */
	public async do(payload: string): Promise<any> {
		return await this.client.request(payload);
	}

	/**
	 * close will stop the server process if it has been launched by
	 * the Typescript SDK.
	 */
	public async close(): Promise<void> {
		if (!this.serverProcess) {
			return;
		}

		this.serverProcess.cancel();
		this.serverProcess.catch((e) => {
			if (!e.isCanceled) {
				console.error('dagger engine error: ', e);
			}
		});
	}
}

export class FSID {
	serial: string;

	constructor(serial: string) {
		this.serial = serial;
	}

	toString(): string {
		return this.serial;
	}

	toJSON(): string {
		return this.serial;
	}
}

export class SecretID {
	serial: string;

	constructor(serial: string) {
		this.serial = serial;
	}

	toString(): string {
		return this.serial;
	}

	toJSON(): string {
		return this.serial;
	}
}
