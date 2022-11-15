import Client from './api/client.js';
/**
 * ConnectOpts defines option used to run cloak
 * in dev mode.
 * Options are based on `dagger cloak` CLI.
 */
export interface ConnectOpts {
    Workdir?: string;
    ConfigPath?: string;
}
declare type CallbackFct = (client: Client) => Promise<void>;
/**
 * connect runs cloak GraphQL server and initializes a
 * GraphQL client to execute query on it through its callback.
 * This implementation is based on the existing Go SDK.
 */
export declare function connect(cb: CallbackFct, config?: ConnectOpts): Promise<void>;
export {};
//# sourceMappingURL=connect.d.ts.map