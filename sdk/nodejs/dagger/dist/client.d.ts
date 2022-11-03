import { GraphQLClient } from 'graphql-request';
import { ServerProcess } from './connect.js';
export declare const client: GraphQLClient;
export declare class Client {
    private client;
    /**
     * hold serverProcess so it can be closed anytime.
     * This is an optional member because users may use this client
     * without launching the server from the Typescript SDK.
     * @private
     */
    private readonly serverProcess?;
    /**
     * creates a new Dagger Typescript SDK GraphQL client.
     * If the client is created by `dagger.connect()`, it will
     * hold the serverProcess, so it can be closed using `close()`
     * method.
     */
    constructor(port?: number, serverProcess?: ServerProcess);
    /**
     * do takes a GraphQL query payload as parameter and send it
     * to Cloak server to execute every operation's in it.
     */
    do(payload: string): Promise<any>;
    /**
     * close will stop the server process if it has been launched by
     * the Typescript SDK.
     */
    close(): Promise<void>;
}
export declare class FSID {
    serial: string;
    constructor(serial: string);
    toString(): string;
    toJSON(): string;
}
export declare class SecretID {
    serial: string;
    constructor(serial: string);
    toString(): string;
    toJSON(): string;
}
//# sourceMappingURL=client.d.ts.map