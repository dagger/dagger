import { Client } from './client.js';
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
export declare type ConnectExecCB = (client: Client) => Promise<void>;
/**
 * connect runs cloak GraphQL server and initializes a
 * GraphQL client to execute query on it through its callback.
 * This implementation is based on the existing Go SDK.
 */
export declare function connect(cb: ConnectExecCB, config?: ConnectOpts): Promise<void>;
//# sourceMappingURL=connect.d.ts.map