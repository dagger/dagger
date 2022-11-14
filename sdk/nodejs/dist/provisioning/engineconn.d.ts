import { GraphQLClient } from "graphql-request";
export interface ConnectOpts {
    Workdir?: string;
    ConfigPath?: string;
}
export interface EngineConn {
    Addr: () => string;
    Connect: (opts: ConnectOpts) => Promise<GraphQLClient>;
    Close: () => Promise<void>;
}
//# sourceMappingURL=engineconn.d.ts.map