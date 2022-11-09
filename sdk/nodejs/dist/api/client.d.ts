import { Scalars, SecretId } from "./types.js";
export declare type QueryTree = {
    operation: string;
    args?: Record<string, any>;
};
interface ClientConfig {
    queryTree?: QueryTree[];
    port?: number;
}
declare class BaseClient {
    protected _queryTree: QueryTree[];
    private client;
    protected port: number;
    constructor({ queryTree, port }?: ClientConfig);
    get queryTree(): QueryTree[];
    protected _compute(): Promise<Record<string, any>>;
}
export default class Client extends BaseClient {
    /**
     * Load a container from ID. Null ID returns an empty container (scratch).
     */
    container(args?: {
        id?: any;
    }): Container;
    /**
     * Construct a cache volume for a given cache key
     */
    cacheVolume(args: {
        key: string;
    }): CacheVolume;
    /**
     * Query a git repository
     */
    git(args: {
        url: string;
    }): Git;
    /**
     * Query the host environment
     */
    host(): Host;
    secret(args: {
        id: Scalars['SecretID'];
    }): Secret;
}
declare class CacheVolume extends BaseClient {
    /**
     * A unique identifier for this container
     */
    id(): Promise<Record<string, Scalars['CacheID']>>;
}
declare class Host extends BaseClient {
    envVariable(args?: {
        name: string;
    }): HostVariable;
    /**
     * The current working directory on the host
     */
    workdir(args?: {
        exclude?: string[] | undefined;
        include?: string[] | undefined;
    }): Directory;
}
declare class HostVariable extends BaseClient {
    secret(): Secret;
    value(): Promise<Record<string, string>>;
}
declare class Secret extends BaseClient {
    id(): Promise<Record<string, SecretId>>;
    plaintext(): Promise<Record<string, string>>;
}
declare class Git extends BaseClient {
    /**
     * Details on one branch
     */
    branch(args: {
        name: string;
    }): Tree;
}
declare class Tree extends BaseClient {
    /**
     * The filesystem tree at this ref
     */
    tree(): Directory;
}
declare class File extends BaseClient {
    /**
   * The contents of the file
   */
    contents(): Promise<Record<string, string>>;
    /**
     * The size of the file, in bytes
     */
    size(): Promise<Record<string, number>>;
}
declare class Container extends BaseClient {
    /**
     * This container after executing the specified command inside it
     */
    exec(args: {
        args?: string[] | undefined;
        stdin?: string | undefined;
        redirectStdout?: string | undefined;
        redirectStderr?: string | undefined;
    }): Container;
    /**
     * Initialize this container from the base image published at the given address
     */
    from(args: {
        address: string;
    }): Container;
    /**
     * This container's root filesystem. Mounts are not included.
     */
    fs(): Directory;
    /**
     * List of paths where a directory is mounted
     */
    mounts(): Promise<Record<string, Array<string>>>;
    /**
     * Initialize this container from this DirectoryID
     */
    withFS(args: {
        id: Scalars['DirectoryID'];
    }): Container;
    /**
     * This container plus a directory mounted at the given path
     */
    withMountedDirectory(args: {
        path: string;
        source: Scalars['DirectoryID'];
    }): Container;
    /**
     * This container plus a cache volume mounted at the given path
     */
    withMountedCache(args: {
        path: string;
        cache: Scalars['CacheID'];
        source?: Scalars['DirectoryID'];
    }): Container;
    /**
     * A unique identifier for this container
     */
    id(): Promise<Record<string, Scalars['ContainerID']>>;
    /**
     * The output stream of the last executed command. Null if no command has been executed.
     */
    stdout(): File;
    /**
     * The error stream of the last executed command. Null if no command has been executed.
     */
    stderr(): File;
    /**
     * This container but with a different working directory
     */
    withWorkdir(args: {
        path: string;
    }): Container;
    /**
     * This container plus an env variable containing the given secret
     * @arg name: string
     * @arg secret: string
     */
    withSecretVariable(args: {
        name: string;
        secret: any;
    }): Container;
    /**
     * This container plus the given environment variable
     */
    withEnvVariable(args: {
        name: string;
        value: string;
    }): Container;
    /**
     * Retrieve a directory at the given path. Mounts are included.
     */
    directory(args: {
        path: string;
    }): Directory;
}
declare class Directory extends BaseClient {
    /**
     * Retrieve a file at the given path
     */
    file(args: {
        path: string;
    }): File;
    /**
     * Retrieve a directory at the given path. Mounts are included.
     */
    id(): Promise<Record<string, Scalars['DirectoryID']>>;
    /**
     * Return a list of files and directories at the given path
     */
    entries(args?: {
        path?: string | undefined;
    }): Promise<Record<string, Array<string>>>;
}
export {};
//# sourceMappingURL=client.d.ts.map