import { ConnectOpts, EngineConn } from '../engineconn.js';
import Client from '../../api/client.js';
/**
 * DockerImage is an implementation of EngineConn to set up a Dagger
 * Engine session from a pulled docker image.
 */
export declare class DockerImage implements EngineConn {
    private imageRef;
    private readonly cacheDir;
    private readonly ENGINE_SESSION_BINARY_PREFIX;
    private enginePid?;
    constructor(u: URL);
    Addr(): string;
    Connect(opts: ConnectOpts): Promise<Client>;
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
    /**
     * pullEngineSessionBin will retrieve Dagger binary from its docker image
     * and copy it to the local host.
     * This function automatically resolves host's platform to copy the correct
     * binary.
     */
    private pullEngineSessionBin;
    /**
     * runEngineSession execute the engine binary and set up a GraphQL client that
     * target this engine.
     */
    private runEngineSession;
    Close(): Promise<void>;
}
//# sourceMappingURL=image.d.ts.map