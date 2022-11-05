import { GraphQLClient } from "graphql-request";
export declare const client: GraphQLClient;
export declare class Client {
    private client;
    constructor();
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