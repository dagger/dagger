import { GraphQLClient } from "graphql-request";
export interface EngineOptions {
    LocalDirs?: Record<string, string>;
    Port?: number;
    Workdir?: string;
    ConfigPath?: string;
}
export declare class Engine {
    private config;
    constructor(config: EngineOptions);
    run(cb: (client: GraphQLClient) => Promise<void>): Promise<void>;
}
//# sourceMappingURL=engine.d.ts.map