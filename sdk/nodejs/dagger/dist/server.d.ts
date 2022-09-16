import type { Client } from "./client.js";
export interface DaggerContext {
    dagger: Client;
}
export declare class DaggerServer {
    resolvers: Record<string, any>;
    constructor(config: {
        resolvers: Record<string, any>;
    });
    run(): void;
}
//# sourceMappingURL=server.d.ts.map