import { Client } from './client.js';
import { ExecaChildProcess } from 'execa';
/**
 * ConnectOpts defines option used to run cloak
 * in dev mode.
 * Options are based on `dagger cloak` CLI.
 */
export interface ConnectOpts {
    LocalDirs?: Record<string, string>;
    Port?: number;
    Workdir?: string;
    ConfigPath?: string;
}
export declare type ServerProcess = ExecaChildProcess;
/**
 * connect runs cloak GraphQL server and initializes a
 * Dagger client.
 * This implementation is based on the existing Go SDK.
 */
export declare function connect(config: ConnectOpts): Promise<Client>;
//# sourceMappingURL=connect.d.ts.map