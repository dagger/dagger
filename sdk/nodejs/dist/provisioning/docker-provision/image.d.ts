import { ConnectOpts, EngineConn } from '../engineconn.js';
import { GraphQLClient } from 'graphql-request';
export declare class DockerImage implements EngineConn {
    private imageRef;
    private readonly cacheDir;
    private readonly ENGINE_SESSION_BINARY_PREFIX;
    constructor(u: URL);
    Addr(): string;
    Connect(opts: ConnectOpts): Promise<GraphQLClient>;
    /**
     * createCacheDir will create a cache directory on user
     * host to store dagger binary.
     *
     * If set, it will use XDG directory, if not, it will use `$HOME/.cache`
     * as base path.
     * Nothing happens if the directory already exists.
     */
    private createCacheDir;
    /**
     * buildBinPath create a path to output engine session binary.
     *
     * It will store it in the cache directory with a name composed
     * of the base engine session as constant and the engine identifier.
     */
    private buildBinPath;
    private pullEngineSessionBin;
    Close(): Promise<void>;
}
//# sourceMappingURL=image.d.ts.map