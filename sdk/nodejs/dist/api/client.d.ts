import { ContainerExecArgs, ContainerWithFsArgs, ContainerWithMountedDirectoryArgs, ContainerWithSecretVariableArgs, ContainerWithWorkdirArgs, DirectoryEntriesArgs, DirectoryFileArgs, GitRepositoryBranchArgs, HostEnvVariableArgs, HostWorkdirArgs, QueryContainerArgs, QueryGitArgs, Scalars, SecretId } from "./types.js";
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
export declare class Client extends BaseClient {
    /**
     * Load a container from ID. Null ID returns an empty container (scratch).
     */
    container(args?: QueryContainerArgs): Container;
    /**
     * Construct a cache volume for a given cache key
     */
    cacheVolume(args: {
        key: Scalars['String'];
    }): CacheVolume;
    /**
     * Query a git repository
     */
    git(args: QueryGitArgs): Git;
    /**
     * Query the host environment
     */
    host(): Host;
}
declare class CacheVolume extends BaseClient {
    /**
     * A unique identifier for this container
     */
    id(): Promise<Record<string, Scalars['CacheID']>>;
}
declare class Host extends BaseClient {
    envVariable(args?: HostEnvVariableArgs): HostVariable;
    /**
     * The current working directory on the host
     */
    workdir(args?: HostWorkdirArgs): Directory;
}
declare class HostVariable extends BaseClient {
    secret(): Secret;
}
declare class Secret extends BaseClient {
    id(): Promise<Record<string, SecretId>>;
}
declare class Git extends BaseClient {
    /**
     * Details on one branch
     */
    branch(args: GitRepositoryBranchArgs): Tree;
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
    exec(args: ContainerExecArgs): Container;
    /**
     * Initialize this container from the base image published at the given address
     */
    from(args: {
        address: Scalars['String'];
    }): Container;
    /**
     * This container's root filesystem. Mounts are not included.
     */
    fs(): Directory;
    /**
     * List of paths where a directory is mounted
     */
    mounts(): Promise<Record<string, Array<Scalars['String']>>>;
    /**
     * Initialize this container from this DirectoryID
     */
    withFS(args: ContainerWithFsArgs): Container;
    /**
     * This container plus a directory mounted at the given path
     */
    withMountedDirectory(args: ContainerWithMountedDirectoryArgs): Container;
    /**
     * This container plus a cache volume mounted at the given path
     */
    withMountedCache(args: {
        path: Scalars['String'];
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
     * This container but with a different working directory
     */
    withWorkdir(args: ContainerWithWorkdirArgs): Container;
    withSecretVariable(args: ContainerWithSecretVariableArgs): Container;
    /**
     * This container plus the given environment variable
     */
    withEnvVariable(args: {
        name: Scalars['String'];
        value: Scalars['String'];
    }): Container;
    /**
     * Retrieve a directory at the given path. Mounts are included.
     */
    directory(args: {
        path: Scalars['String'];
    }): Directory;
}
declare class Directory extends BaseClient {
    /**
     * Retrieve a file at the given path
     */
    file(args: DirectoryFileArgs): File;
    /**
     * Retrieve a directory at the given path. Mounts are included.
     */
    id(): Promise<Record<string, Scalars['DirectoryID']>>;
    /**
     * Return a list of files and directories at the given path
     */
    entries(args?: DirectoryEntriesArgs): Promise<Record<string, Array<Scalars['String']>>>;
}
export {};
//# sourceMappingURL=client.d.ts.map