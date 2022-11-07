import { GraphQLClient } from 'graphql-request';
export declare const client: GraphQLClient;
export declare class Client {
    private client;
    /**
     * creates a new Dagger Typescript SDK GraphQL client.
     */
    constructor(port?: number);
    /**
     * do takes a GraphQL query payload as parameter and send it
     * to Cloak server to execute every operation's in it.
     */
    do(payload: string): Promise<any>;
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