import { Client } from "./client.js";
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
export declare function connect(config?: ConnectOpts): Promise<Client>;
//# sourceMappingURL=connect.d.ts.map