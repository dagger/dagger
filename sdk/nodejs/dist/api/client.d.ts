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
/**
 * A global cache volume identifier
 */
export declare type CacheID = any;
/**
 * A directory whose contents persist across runs
 */
declare class CacheVolume extends BaseClient {
    id(): Promise<Record<string, CacheID>>;
}
export declare type ContainerBuildArgs = {
    context: DirectoryID;
    dockerfile?: string;
};
export declare type ContainerDirectoryArgs = {
    path: string;
};
export declare type ContainerEnvVariableArgs = {
    name: string;
};
export declare type ContainerExecArgs = {
    args?: string[];
    stdin?: string;
    redirectStdout?: string;
    redirectStderr?: string;
    experimentalPrivilegedNesting?: boolean;
};
export declare type ContainerExportArgs = {
    path: string;
    platformVariants?: ContainerID[];
};
export declare type ContainerFileArgs = {
    path: string;
};
export declare type ContainerFromArgs = {
    address: string;
};
export declare type ContainerPublishArgs = {
    address: string;
    platformVariants?: ContainerID[];
};
export declare type ContainerWithDefaultArgsArgs = {
    args?: string[];
};
export declare type ContainerWithEntrypointArgs = {
    args: string[];
};
export declare type ContainerWithEnvVariableArgs = {
    name: string;
    value: string;
};
export declare type ContainerWithFSArgs = {
    id: DirectoryID;
};
export declare type ContainerWithMountedCacheArgs = {
    path: string;
    cache: CacheID;
    source?: DirectoryID;
};
export declare type ContainerWithMountedDirectoryArgs = {
    path: string;
    source: DirectoryID;
};
export declare type ContainerWithMountedFileArgs = {
    path: string;
    source: FileID;
};
export declare type ContainerWithMountedSecretArgs = {
    path: string;
    source: SecretID;
};
export declare type ContainerWithMountedTempArgs = {
    path: string;
};
export declare type ContainerWithSecretVariableArgs = {
    name: string;
    secret: SecretID;
};
export declare type ContainerWithUserArgs = {
    name: string;
};
export declare type ContainerWithWorkdirArgs = {
    path: string;
};
export declare type ContainerWithoutEnvVariableArgs = {
    name: string;
};
export declare type ContainerWithoutMountArgs = {
    path: string;
};
/**
 * An OCI-compatible container, also known as a docker container
 */
declare class Container extends BaseClient {
    /**
     * Initialize this container from a Dockerfile build
     */
    build(args: ContainerBuildArgs): Container;
    /**
     * Default arguments for future commands
     */
    defaultArgs(): Promise<Record<string, string[]>>;
    /**
     * Retrieve a directory at the given path. Mounts are included.
     */
    directory(args: ContainerDirectoryArgs): Directory;
    /**
     * Entrypoint to be prepended to the arguments of all commands
     */
    entrypoint(): Promise<Record<string, string[]>>;
    /**
     * The value of the specified environment variable
     */
    envVariable(args: ContainerEnvVariableArgs): Promise<Record<string, string>>;
    /**
     * A list of environment variables passed to commands
     */
    envVariables(): Promise<Record<string, EnvVariable[]>>;
    /**
     * This container after executing the specified command inside it
     */
    exec(args?: ContainerExecArgs): Container;
    /**
     * Exit code of the last executed command. Zero means success.
     * Null if no command has been executed.
     */
    exitCode(): Promise<Record<string, number>>;
    /**
     * Write the container as an OCI tarball to the destination file path on the host
     */
    export(args: ContainerExportArgs): Promise<Record<string, boolean>>;
    /**
     * Retrieve a file at the given path. Mounts are included.
     */
    file(args: ContainerFileArgs): File;
    /**
     * Initialize this container from the base image published at the given address
     */
    from(args: ContainerFromArgs): Container;
    /**
     * This container's root filesystem. Mounts are not included.
     */
    fs(): Directory;
    /**
     * A unique identifier for this container
     */
    id(): Promise<Record<string, ContainerID>>;
    /**
     * List of paths where a directory is mounted
     */
    mounts(): Promise<Record<string, string[]>>;
    /**
     * The platform this container executes and publishes as
     */
    platform(): Promise<Record<string, Platform>>;
    /**
     * Publish this container as a new image, returning a fully qualified ref
     */
    publish(args: ContainerPublishArgs): Promise<Record<string, string>>;
    /**
     * The error stream of the last executed command.
     * Null if no command has been executed.
     */
    stderr(): File;
    /**
     * The output stream of the last executed command.
     * Null if no command has been executed.
     */
    stdout(): File;
    /**
     * The user to be set for all commands
     */
    user(): Promise<Record<string, string>>;
    /**
     * Configures default arguments for future commands
     */
    withDefaultArgs(args?: ContainerWithDefaultArgsArgs): Container;
    /**
     * This container but with a different command entrypoint
     */
    withEntrypoint(args: ContainerWithEntrypointArgs): Container;
    /**
     * This container plus the given environment variable
     */
    withEnvVariable(args: ContainerWithEnvVariableArgs): Container;
    /**
     * Initialize this container from this DirectoryID
     */
    withFS(args: ContainerWithFSArgs): Container;
    /**
     * This container plus a cache volume mounted at the given path
     */
    withMountedCache(args: ContainerWithMountedCacheArgs): Container;
    /**
     * This container plus a directory mounted at the given path
     */
    withMountedDirectory(args: ContainerWithMountedDirectoryArgs): Container;
    /**
     * This container plus a file mounted at the given path
     */
    withMountedFile(args: ContainerWithMountedFileArgs): Container;
    /**
     * This container plus a secret mounted into a file at the given path
     */
    withMountedSecret(args: ContainerWithMountedSecretArgs): Container;
    /**
     * This container plus a temporary directory mounted at the given path
     */
    withMountedTemp(args: ContainerWithMountedTempArgs): Container;
    /**
     * This container plus an env variable containing the given secret
     */
    withSecretVariable(args: ContainerWithSecretVariableArgs): Container;
    /**
     * This container but with a different command user
     */
    withUser(args: ContainerWithUserArgs): Container;
    /**
     * This container but with a different working directory
     */
    withWorkdir(args: ContainerWithWorkdirArgs): Container;
    /**
     * This container minus the given environment variable
     */
    withoutEnvVariable(args: ContainerWithoutEnvVariableArgs): Container;
    /**
     * This container after unmounting everything at the given path.
     */
    withoutMount(args: ContainerWithoutMountArgs): Container;
    /**
     * The working directory for all commands
     */
    workdir(): Promise<Record<string, string>>;
}
/**
 * A unique container identifier. Null designates an empty container (scratch).
 */
export declare type ContainerID = any;
/**
 * The `DateTime` scalar type represents a DateTime. The DateTime is serialized as an RFC 3339 quoted string
 */
export declare type DateTime = any;
export declare type DirectoryDiffArgs = {
    other: DirectoryID;
};
export declare type DirectoryDirectoryArgs = {
    path: string;
};
export declare type DirectoryEntriesArgs = {
    path?: string;
};
export declare type DirectoryExportArgs = {
    path: string;
};
export declare type DirectoryFileArgs = {
    path: string;
};
export declare type DirectoryLoadProjectArgs = {
    configPath: string;
};
export declare type DirectoryWithDirectoryArgs = {
    path: string;
    directory: DirectoryID;
    exclude?: string[];
    include?: string[];
};
export declare type DirectoryWithFileArgs = {
    path: string;
    source: FileID;
};
export declare type DirectoryWithNewDirectoryArgs = {
    path: string;
};
export declare type DirectoryWithNewFileArgs = {
    path: string;
    contents?: string;
};
export declare type DirectoryWithoutDirectoryArgs = {
    path: string;
};
export declare type DirectoryWithoutFileArgs = {
    path: string;
};
/**
 * A directory
 */
declare class Directory extends BaseClient {
    /**
     * The difference between this directory and an another directory
     */
    diff(args: DirectoryDiffArgs): Directory;
    /**
     * Retrieve a directory at the given path
     */
    directory(args: DirectoryDirectoryArgs): Directory;
    /**
     * Return a list of files and directories at the given path
     */
    entries(args?: DirectoryEntriesArgs): Promise<Record<string, string[]>>;
    /**
     * Write the contents of the directory to a path on the host
     */
    export(args: DirectoryExportArgs): Promise<Record<string, boolean>>;
    /**
     * Retrieve a file at the given path
     */
    file(args: DirectoryFileArgs): File;
    /**
     * The content-addressed identifier of the directory
     */
    id(): Promise<Record<string, DirectoryID>>;
    /**
     * load a project's metadata
     */
    loadProject(args: DirectoryLoadProjectArgs): Project;
    /**
     * This directory plus a directory written at the given path
     */
    withDirectory(args: DirectoryWithDirectoryArgs): Directory;
    /**
     * This directory plus the contents of the given file copied to the given path
     */
    withFile(args: DirectoryWithFileArgs): Directory;
    /**
     * This directory plus a new directory created at the given path
     */
    withNewDirectory(args: DirectoryWithNewDirectoryArgs): Directory;
    /**
     * This directory plus a new file written at the given path
     */
    withNewFile(args: DirectoryWithNewFileArgs): Directory;
    /**
     * This directory with the directory at the given path removed
     */
    withoutDirectory(args: DirectoryWithoutDirectoryArgs): Directory;
    /**
     * This directory with the file at the given path removed
     */
    withoutFile(args: DirectoryWithoutFileArgs): Directory;
}
/**
 * A content-addressed directory identifier
 */
export declare type DirectoryID = any;
/**
 * EnvVariable is a simple key value object that represents an environment variable.
 */
declare class EnvVariable extends BaseClient {
    /**
     * name is the environment variable name.
     */
    name(): Promise<Record<string, string>>;
    /**
     * value is the environment variable value
     */
    value(): Promise<Record<string, string>>;
}
export declare type FileExportArgs = {
    path: string;
};
/**
 * A file
 */
declare class File extends BaseClient {
    /**
     * The contents of the file
     */
    contents(): Promise<Record<string, string>>;
    /**
     * Write the file to a file path on the host
     */
    export(args: FileExportArgs): Promise<Record<string, boolean>>;
    /**
     * The content-addressed identifier of the file
     */
    id(): Promise<Record<string, FileID>>;
    secret(): Secret;
    /**
     * The size of the file, in bytes
     */
    size(): Promise<Record<string, number>>;
}
export declare type FileID = any;
/**
 * A git ref (tag or branch)
 */
declare class GitRef extends BaseClient {
    /**
     * The digest of the current value of this ref
     */
    digest(): Promise<Record<string, string>>;
    /**
     * The filesystem tree at this ref
     */
    tree(): Directory;
}
export declare type GitRepositoryBranchArgs = {
    name: string;
};
export declare type GitRepositoryCommitArgs = {
    id: string;
};
export declare type GitRepositoryTagArgs = {
    name: string;
};
/**
 * A git repository
 */
declare class GitRepository extends BaseClient {
    /**
     * Details on one branch
     */
    branch(args: GitRepositoryBranchArgs): GitRef;
    /**
     * List of branches on the repository
     */
    branches(): Promise<Record<string, string[]>>;
    /**
     * Details on one commit
     */
    commit(args: GitRepositoryCommitArgs): GitRef;
    /**
     * Details on one tag
     */
    tag(args: GitRepositoryTagArgs): GitRef;
    /**
     * List of tags on the repository
     */
    tags(): Promise<Record<string, string[]>>;
}
export declare type HostDirectoryArgs = {
    path: string;
    exclude?: string[];
    include?: string[];
};
export declare type HostEnvVariableArgs = {
    name: string;
};
export declare type HostWorkdirArgs = {
    exclude?: string[];
    include?: string[];
};
/**
 * Information about the host execution environment
 */
declare class Host extends BaseClient {
    /**
     * Access a directory on the host
     */
    directory(args: HostDirectoryArgs): Directory;
    /**
     * Lookup the value of an environment variable. Null if the variable is not available.
     */
    envVariable(args: HostEnvVariableArgs): HostVariable;
    /**
     * The current working directory on the host
     */
    workdir(args?: HostWorkdirArgs): Directory;
}
/**
 * An environment variable on the host environment
 */
declare class HostVariable extends BaseClient {
    /**
     * A secret referencing the value of this variable
     */
    secret(): Secret;
    /**
     * The value of this variable
     */
    value(): Promise<Record<string, string>>;
}
/**
 * The `ID` scalar type represents a unique identifier, often used to refetch an object or as key for a cache. The ID type appears in a JSON response as a String; however, it is not intended to be human-readable. When expected as an input type, any string (such as `"4"`) or integer (such as `4`) input value will be accepted as an ID.
 */
export declare type ID = any;
export declare type Platform = any;
/**
 * A set of scripts and/or extensions
 */
declare class Project extends BaseClient {
    /**
     * extensions in this project
     */
    extensions(): Promise<Record<string, Project[]>>;
    /**
     * Code files generated by the SDKs in the project
     */
    generatedCode(): Directory;
    /**
     * install the project's schema
     */
    install(): Promise<Record<string, boolean>>;
    /**
     * name of the project
     */
    name(): Promise<Record<string, string>>;
    /**
     * schema provided by the project
     */
    schema(): Promise<Record<string, string>>;
    /**
     * sdk used to generate code for and/or execute this project
     */
    sdk(): Promise<Record<string, string>>;
}
export declare type ClientCacheVolumeArgs = {
    key: string;
};
export declare type ClientContainerArgs = {
    id?: ContainerID;
    platform?: Platform;
};
export declare type ClientDirectoryArgs = {
    id?: DirectoryID;
};
export declare type ClientFileArgs = {
    id: FileID;
};
export declare type ClientGitArgs = {
    url: string;
    keepGitDir?: boolean;
};
export declare type ClientHttpArgs = {
    url: string;
};
export declare type ClientProjectArgs = {
    name: string;
};
export declare type ClientSecretArgs = {
    id: SecretID;
};
export default class Client extends BaseClient {
    /**
     * Construct a cache volume for a given cache key
     */
    cacheVolume(args: ClientCacheVolumeArgs): CacheVolume;
    /**
     * Load a container from ID.
     * Null ID returns an empty container (scratch).
     * Optional platform argument initializes new containers to execute and publish as that platform. Platform defaults to that of the builder's host.
     */
    container(args?: ClientContainerArgs): Container;
    /**
     * The default platform of the builder.
     */
    defaultPlatform(): Promise<Record<string, Platform>>;
    /**
     * Load a directory by ID. No argument produces an empty directory.
     */
    directory(args?: ClientDirectoryArgs): Directory;
    /**
     * Load a file by ID
     */
    file(args: ClientFileArgs): File;
    /**
     * Query a git repository
     */
    git(args: ClientGitArgs): GitRepository;
    /**
     * Query the host environment
     */
    host(): Host;
    /**
     * An http remote
     */
    http(args: ClientHttpArgs): File;
    /**
     * Look up a project by name
     */
    project(args: ClientProjectArgs): Project;
    /**
     * Load a secret from its ID
     */
    secret(args: ClientSecretArgs): Secret;
}
/**
 * A reference to a secret value, which can be handled more safely than the value itself
 */
declare class Secret extends BaseClient {
    /**
     * The identifier for this secret
     */
    id(): Promise<Record<string, SecretID>>;
    /**
     * The value of this secret
     */
    plaintext(): Promise<Record<string, string>>;
}
/**
 * A unique identifier for a secret
 */
export declare type SecretID = any;
export {};
//# sourceMappingURL=client.d.ts.map