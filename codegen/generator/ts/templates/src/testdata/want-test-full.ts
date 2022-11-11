import { GraphQLClient, gql } from "../index.js";
import { queryBuilder, queryFlatten } from "./utils.js"

export type QueryTree = {
  operation: string
  args?: Record<string, any>
}

interface ClientConfig {
  queryTree?: QueryTree[],
  port?: number
}

class BaseClient {
  protected _queryTree:  QueryTree[]
	private client: GraphQLClient;
  protected port: number


  constructor({queryTree, port}: ClientConfig = {}) {
    this._queryTree = queryTree || []
    this.port = port || 8080
		this.client = new GraphQLClient(`http://localhost:${port}/query`);
  }

  get queryTree() {
    return this._queryTree;
  }

  protected async _compute() : Promise<Record<string, any>> {
    // run the query and return the result.
    const query = queryBuilder(this._queryTree)

    const computeQuery: Promise<Record<string, string>> = new Promise(async (resolve, reject) => {
      const response: Awaited<Promise<Record<string, any>>> = await this.client.request(gql`${query}`).catch((error) => {reject(console.error(JSON.stringify(error, undefined, 2)))})

      resolve(queryFlatten(response));
    })

    const result = await computeQuery;

    return result
  }
}




/**
 * A global cache volume identifier
 */
export type CacheID = any




/**
 * A directory whose contents persist across runs
 */
class CacheVolume extends BaseClient {
  async id(): Promise<Record<string, CacheID>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'id'
      }
    ]

    const response: Awaited<Record<string, CacheID>> = await this._compute()

    return response
  }
}


export type ContainerBuildArgs = {
  context: DirectoryID;
  dockerfile?: string;
};

export type ContainerDirectoryArgs = {
  path: string;
};

export type ContainerEnvVariableArgs = {
  name: string;
};

export type ContainerExecArgs = {
  args?: string[];
  stdin?: string;
  redirectStdout?: string;
  redirectStderr?: string;
  experimentalPrivilegedNesting?: boolean;
};

export type ContainerExportArgs = {
  path: string;
  platformVariants?: ContainerID[];
};

export type ContainerFileArgs = {
  path: string;
};

export type ContainerFromArgs = {
  address: string;
};

export type ContainerPublishArgs = {
  address: string;
  platformVariants?: ContainerID[];
};

export type ContainerWithDefaultArgsArgs = {
  args?: string[];
};

export type ContainerWithEntrypointArgs = {
  args: string[];
};

export type ContainerWithEnvVariableArgs = {
  name: string;
  value: string;
};

export type ContainerWithFSArgs = {
  id: DirectoryID;
};

export type ContainerWithMountedCacheArgs = {
  path: string;
  cache: CacheID;
  source?: DirectoryID;
};

export type ContainerWithMountedDirectoryArgs = {
  path: string;
  source: DirectoryID;
};

export type ContainerWithMountedFileArgs = {
  path: string;
  source: FileID;
};

export type ContainerWithMountedSecretArgs = {
  path: string;
  source: SecretID;
};

export type ContainerWithMountedTempArgs = {
  path: string;
};

export type ContainerWithSecretVariableArgs = {
  name: string;
  secret: SecretID;
};

export type ContainerWithUserArgs = {
  name: string;
};

export type ContainerWithWorkdirArgs = {
  path: string;
};

export type ContainerWithoutEnvVariableArgs = {
  name: string;
};

export type ContainerWithoutMountArgs = {
  path: string;
};

/**
 * An OCI-compatible container, also known as a docker container
 */
class Container extends BaseClient {
  /**
   * Initialize this container from a Dockerfile build
   */
  build(args: ContainerBuildArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'build',
      args
      }
    ], port: this.port})
  }
  /**
   * Default arguments for future commands
   */
  async defaultArgs(): Promise<Record<string, string[]>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'defaultArgs'
      }
    ]

    const response: Awaited<Record<string, string[]>> = await this._compute()

    return response
  }
  /**
   * Retrieve a directory at the given path. Mounts are included.
   */
  directory(args: ContainerDirectoryArgs): Directory {
    return new Directory({queryTree: [
      ...this._queryTree,
      {
      operation: 'directory',
      args
      }
    ], port: this.port})
  }
  /**
   * Entrypoint to be prepended to the arguments of all commands
   */
  async entrypoint(): Promise<Record<string, string[]>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'entrypoint'
      }
    ]

    const response: Awaited<Record<string, string[]>> = await this._compute()

    return response
  }
  /**
   * The value of the specified environment variable
   */
  async envVariable(args: ContainerEnvVariableArgs): Promise<Record<string, string>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'envVariable',
      args
      }
    ]

    const response: Awaited<Record<string, string>> = await this._compute()

    return response
  }
  /**
   * A list of environment variables passed to commands
   */
  async envVariables(): Promise<Record<string, EnvVariable[]>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'envVariables'
      }
    ]

    const response: Awaited<Record<string, EnvVariable[]>> = await this._compute()

    return response
  }
  /**
   * This container after executing the specified command inside it
   */
  exec(args?: ContainerExecArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'exec',
      args
      }
    ], port: this.port})
  }
  /**
   * Exit code of the last executed command. Zero means success.
   * Null if no command has been executed.
   */
  async exitCode(): Promise<Record<string, number>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'exitCode'
      }
    ]

    const response: Awaited<Record<string, number>> = await this._compute()

    return response
  }
  /**
   * Write the container as an OCI tarball to the destination file path on the host
   */
  async export(args: ContainerExportArgs): Promise<Record<string, boolean>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'export',
      args
      }
    ]

    const response: Awaited<Record<string, boolean>> = await this._compute()

    return response
  }
  /**
   * Retrieve a file at the given path. Mounts are included.
   */
  file(args: ContainerFileArgs): File {
    return new File({queryTree: [
      ...this._queryTree,
      {
      operation: 'file',
      args
      }
    ], port: this.port})
  }
  /**
   * Initialize this container from the base image published at the given address
   */
  from(args: ContainerFromArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'from',
      args
      }
    ], port: this.port})
  }
  /**
   * This container's root filesystem. Mounts are not included.
   */
  fs(): Directory {
    return new Directory({queryTree: [
      ...this._queryTree,
      {
      operation: 'fs'
      }
    ], port: this.port})
  }
  /**
   * A unique identifier for this container
   */
  async id(): Promise<Record<string, ContainerID>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'id'
      }
    ]

    const response: Awaited<Record<string, ContainerID>> = await this._compute()

    return response
  }
  /**
   * List of paths where a directory is mounted
   */
  async mounts(): Promise<Record<string, string[]>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'mounts'
      }
    ]

    const response: Awaited<Record<string, string[]>> = await this._compute()

    return response
  }
  /**
   * The platform this container executes and publishes as
   */
  async platform(): Promise<Record<string, Platform>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'platform'
      }
    ]

    const response: Awaited<Record<string, Platform>> = await this._compute()

    return response
  }
  /**
   * Publish this container as a new image, returning a fully qualified ref
   */
  async publish(args: ContainerPublishArgs): Promise<Record<string, string>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'publish',
      args
      }
    ]

    const response: Awaited<Record<string, string>> = await this._compute()

    return response
  }
  /**
   * The error stream of the last executed command.
   * Null if no command has been executed.
   */
  stderr(): File {
    return new File({queryTree: [
      ...this._queryTree,
      {
      operation: 'stderr'
      }
    ], port: this.port})
  }
  /**
   * The output stream of the last executed command.
   * Null if no command has been executed.
   */
  stdout(): File {
    return new File({queryTree: [
      ...this._queryTree,
      {
      operation: 'stdout'
      }
    ], port: this.port})
  }
  /**
   * The user to be set for all commands
   */
  async user(): Promise<Record<string, string>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'user'
      }
    ]

    const response: Awaited<Record<string, string>> = await this._compute()

    return response
  }
  /**
   * Configures default arguments for future commands
   */
  withDefaultArgs(args?: ContainerWithDefaultArgsArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withDefaultArgs',
      args
      }
    ], port: this.port})
  }
  /**
   * This container but with a different command entrypoint
   */
  withEntrypoint(args: ContainerWithEntrypointArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withEntrypoint',
      args
      }
    ], port: this.port})
  }
  /**
   * This container plus the given environment variable
   */
  withEnvVariable(args: ContainerWithEnvVariableArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withEnvVariable',
      args
      }
    ], port: this.port})
  }
  /**
   * Initialize this container from this DirectoryID
   */
  withFS(args: ContainerWithFSArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withFS',
      args
      }
    ], port: this.port})
  }
  /**
   * This container plus a cache volume mounted at the given path
   */
  withMountedCache(args: ContainerWithMountedCacheArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withMountedCache',
      args
      }
    ], port: this.port})
  }
  /**
   * This container plus a directory mounted at the given path
   */
  withMountedDirectory(args: ContainerWithMountedDirectoryArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withMountedDirectory',
      args
      }
    ], port: this.port})
  }
  /**
   * This container plus a file mounted at the given path
   */
  withMountedFile(args: ContainerWithMountedFileArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withMountedFile',
      args
      }
    ], port: this.port})
  }
  /**
   * This container plus a secret mounted into a file at the given path
   */
  withMountedSecret(args: ContainerWithMountedSecretArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withMountedSecret',
      args
      }
    ], port: this.port})
  }
  /**
   * This container plus a temporary directory mounted at the given path
   */
  withMountedTemp(args: ContainerWithMountedTempArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withMountedTemp',
      args
      }
    ], port: this.port})
  }
  /**
   * This container plus an env variable containing the given secret
   */
  withSecretVariable(args: ContainerWithSecretVariableArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withSecretVariable',
      args
      }
    ], port: this.port})
  }
  /**
   * This container but with a different command user
   */
  withUser(args: ContainerWithUserArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withUser',
      args
      }
    ], port: this.port})
  }
  /**
   * This container but with a different working directory
   */
  withWorkdir(args: ContainerWithWorkdirArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withWorkdir',
      args
      }
    ], port: this.port})
  }
  /**
   * This container minus the given environment variable
   */
  withoutEnvVariable(args: ContainerWithoutEnvVariableArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withoutEnvVariable',
      args
      }
    ], port: this.port})
  }
  /**
   * This container after unmounting everything at the given path.
   */
  withoutMount(args: ContainerWithoutMountArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withoutMount',
      args
      }
    ], port: this.port})
  }
  /**
   * The working directory for all commands
   */
  async workdir(): Promise<Record<string, string>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'workdir'
      }
    ]

    const response: Awaited<Record<string, string>> = await this._compute()

    return response
  }
}


/**
 * A unique container identifier. Null designates an empty container (scratch).
 */
export type ContainerID = any




/**
 * The `DateTime` scalar type represents a DateTime. The DateTime is serialized as an RFC 3339 quoted string
 */
export type DateTime = any




export type DirectoryDiffArgs = {
  other: DirectoryID;
};

export type DirectoryDirectoryArgs = {
  path: string;
};

export type DirectoryEntriesArgs = {
  path?: string;
};

export type DirectoryExportArgs = {
  path: string;
};

export type DirectoryFileArgs = {
  path: string;
};

export type DirectoryLoadProjectArgs = {
  configPath: string;
};

export type DirectoryWithDirectoryArgs = {
  path: string;
  directory: DirectoryID;
  exclude?: string[];
  include?: string[];
};

export type DirectoryWithFileArgs = {
  path: string;
  source: FileID;
};

export type DirectoryWithNewDirectoryArgs = {
  path: string;
};

export type DirectoryWithNewFileArgs = {
  path: string;
  contents?: string;
};

export type DirectoryWithoutDirectoryArgs = {
  path: string;
};

export type DirectoryWithoutFileArgs = {
  path: string;
};

/**
 * A directory
 */
class Directory extends BaseClient {
  /**
   * The difference between this directory and an another directory
   */
  diff(args: DirectoryDiffArgs): Directory {
    return new Directory({queryTree: [
      ...this._queryTree,
      {
      operation: 'diff',
      args
      }
    ], port: this.port})
  }
  /**
   * Retrieve a directory at the given path
   */
  directory(args: DirectoryDirectoryArgs): Directory {
    return new Directory({queryTree: [
      ...this._queryTree,
      {
      operation: 'directory',
      args
      }
    ], port: this.port})
  }
  /**
   * Return a list of files and directories at the given path
   */
  async entries(args?: DirectoryEntriesArgs): Promise<Record<string, string[]>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'entries',
      args
      }
    ]

    const response: Awaited<Record<string, string[]>> = await this._compute()

    return response
  }
  /**
   * Write the contents of the directory to a path on the host
   */
  async export(args: DirectoryExportArgs): Promise<Record<string, boolean>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'export',
      args
      }
    ]

    const response: Awaited<Record<string, boolean>> = await this._compute()

    return response
  }
  /**
   * Retrieve a file at the given path
   */
  file(args: DirectoryFileArgs): File {
    return new File({queryTree: [
      ...this._queryTree,
      {
      operation: 'file',
      args
      }
    ], port: this.port})
  }
  /**
   * The content-addressed identifier of the directory
   */
  async id(): Promise<Record<string, DirectoryID>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'id'
      }
    ]

    const response: Awaited<Record<string, DirectoryID>> = await this._compute()

    return response
  }
  /**
   * load a project's metadata
   */
  loadProject(args: DirectoryLoadProjectArgs): Project {
    return new Project({queryTree: [
      ...this._queryTree,
      {
      operation: 'loadProject',
      args
      }
    ], port: this.port})
  }
  /**
   * This directory plus a directory written at the given path
   */
  withDirectory(args: DirectoryWithDirectoryArgs): Directory {
    return new Directory({queryTree: [
      ...this._queryTree,
      {
      operation: 'withDirectory',
      args
      }
    ], port: this.port})
  }
  /**
   * This directory plus the contents of the given file copied to the given path
   */
  withFile(args: DirectoryWithFileArgs): Directory {
    return new Directory({queryTree: [
      ...this._queryTree,
      {
      operation: 'withFile',
      args
      }
    ], port: this.port})
  }
  /**
   * This directory plus a new directory created at the given path
   */
  withNewDirectory(args: DirectoryWithNewDirectoryArgs): Directory {
    return new Directory({queryTree: [
      ...this._queryTree,
      {
      operation: 'withNewDirectory',
      args
      }
    ], port: this.port})
  }
  /**
   * This directory plus a new file written at the given path
   */
  withNewFile(args: DirectoryWithNewFileArgs): Directory {
    return new Directory({queryTree: [
      ...this._queryTree,
      {
      operation: 'withNewFile',
      args
      }
    ], port: this.port})
  }
  /**
   * This directory with the directory at the given path removed
   */
  withoutDirectory(args: DirectoryWithoutDirectoryArgs): Directory {
    return new Directory({queryTree: [
      ...this._queryTree,
      {
      operation: 'withoutDirectory',
      args
      }
    ], port: this.port})
  }
  /**
   * This directory with the file at the given path removed
   */
  withoutFile(args: DirectoryWithoutFileArgs): Directory {
    return new Directory({queryTree: [
      ...this._queryTree,
      {
      operation: 'withoutFile',
      args
      }
    ], port: this.port})
  }
}


/**
 * A content-addressed directory identifier
 */
export type DirectoryID = any




/**
 * EnvVariable is a simple key value object that represents an environment variable.
 */
class EnvVariable extends BaseClient {
  /**
   * name is the environment variable name.
   */
  async name(): Promise<Record<string, string>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'name'
      }
    ]

    const response: Awaited<Record<string, string>> = await this._compute()

    return response
  }
  /**
   * value is the environment variable value
   */
  async value(): Promise<Record<string, string>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'value'
      }
    ]

    const response: Awaited<Record<string, string>> = await this._compute()

    return response
  }
}


export type FileExportArgs = {
  path: string;
};

/**
 * A file
 */
class File extends BaseClient {
  /**
   * The contents of the file
   */
  async contents(): Promise<Record<string, string>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'contents'
      }
    ]

    const response: Awaited<Record<string, string>> = await this._compute()

    return response
  }
  /**
   * Write the file to a file path on the host
   */
  async export(args: FileExportArgs): Promise<Record<string, boolean>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'export',
      args
      }
    ]

    const response: Awaited<Record<string, boolean>> = await this._compute()

    return response
  }
  /**
   * The content-addressed identifier of the file
   */
  async id(): Promise<Record<string, FileID>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'id'
      }
    ]

    const response: Awaited<Record<string, FileID>> = await this._compute()

    return response
  }
  secret(): Secret {
    return new Secret({queryTree: [
      ...this._queryTree,
      {
      operation: 'secret'
      }
    ], port: this.port})
  }
  /**
   * The size of the file, in bytes
   */
  async size(): Promise<Record<string, number>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'size'
      }
    ]

    const response: Awaited<Record<string, number>> = await this._compute()

    return response
  }
}


export type FileID = any







/**
 * A git ref (tag or branch)
 */
class GitRef extends BaseClient {
  /**
   * The digest of the current value of this ref
   */
  async digest(): Promise<Record<string, string>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'digest'
      }
    ]

    const response: Awaited<Record<string, string>> = await this._compute()

    return response
  }
  /**
   * The filesystem tree at this ref
   */
  tree(): Directory {
    return new Directory({queryTree: [
      ...this._queryTree,
      {
      operation: 'tree'
      }
    ], port: this.port})
  }
}


export type GitRepositoryBranchArgs = {
  name: string;
};

export type GitRepositoryCommitArgs = {
  id: string;
};

export type GitRepositoryTagArgs = {
  name: string;
};

/**
 * A git repository
 */
class GitRepository extends BaseClient {
  /**
   * Details on one branch
   */
  branch(args: GitRepositoryBranchArgs): GitRef {
    return new GitRef({queryTree: [
      ...this._queryTree,
      {
      operation: 'branch',
      args
      }
    ], port: this.port})
  }
  /**
   * List of branches on the repository
   */
  async branches(): Promise<Record<string, string[]>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'branches'
      }
    ]

    const response: Awaited<Record<string, string[]>> = await this._compute()

    return response
  }
  /**
   * Details on one commit
   */
  commit(args: GitRepositoryCommitArgs): GitRef {
    return new GitRef({queryTree: [
      ...this._queryTree,
      {
      operation: 'commit',
      args
      }
    ], port: this.port})
  }
  /**
   * Details on one tag
   */
  tag(args: GitRepositoryTagArgs): GitRef {
    return new GitRef({queryTree: [
      ...this._queryTree,
      {
      operation: 'tag',
      args
      }
    ], port: this.port})
  }
  /**
   * List of tags on the repository
   */
  async tags(): Promise<Record<string, string[]>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'tags'
      }
    ]

    const response: Awaited<Record<string, string[]>> = await this._compute()

    return response
  }
}


export type HostDirectoryArgs = {
  path: string;
  exclude?: string[];
  include?: string[];
};

export type HostEnvVariableArgs = {
  name: string;
};

export type HostWorkdirArgs = {
  exclude?: string[];
  include?: string[];
};

/**
 * Information about the host execution environment
 */
class Host extends BaseClient {
  /**
   * Access a directory on the host
   */
  directory(args: HostDirectoryArgs): Directory {
    return new Directory({queryTree: [
      ...this._queryTree,
      {
      operation: 'directory',
      args
      }
    ], port: this.port})
  }
  /**
   * Lookup the value of an environment variable. Null if the variable is not available.
   */
  envVariable(args: HostEnvVariableArgs): HostVariable {
    return new HostVariable({queryTree: [
      ...this._queryTree,
      {
      operation: 'envVariable',
      args
      }
    ], port: this.port})
  }
  /**
   * The current working directory on the host
   */
  workdir(args?: HostWorkdirArgs): Directory {
    return new Directory({queryTree: [
      ...this._queryTree,
      {
      operation: 'workdir',
      args
      }
    ], port: this.port})
  }
}


/**
 * An environment variable on the host environment
 */
class HostVariable extends BaseClient {
  /**
   * A secret referencing the value of this variable
   */
  secret(): Secret {
    return new Secret({queryTree: [
      ...this._queryTree,
      {
      operation: 'secret'
      }
    ], port: this.port})
  }
  /**
   * The value of this variable
   */
  async value(): Promise<Record<string, string>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'value'
      }
    ]

    const response: Awaited<Record<string, string>> = await this._compute()

    return response
  }
}


/**
 * The `ID` scalar type represents a unique identifier, often used to refetch an object or as key for a cache. The ID type appears in a JSON response as a String; however, it is not intended to be human-readable. When expected as an input type, any string (such as `"4"`) or integer (such as `4`) input value will be accepted as an ID.
 */
export type ID = any







export type Platform = any




/**
 * A set of scripts and/or extensions
 */
class Project extends BaseClient {
  /**
   * extensions in this project
   */
  async extensions(): Promise<Record<string, Project[]>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'extensions'
      }
    ]

    const response: Awaited<Record<string, Project[]>> = await this._compute()

    return response
  }
  /**
   * Code files generated by the SDKs in the project
   */
  generatedCode(): Directory {
    return new Directory({queryTree: [
      ...this._queryTree,
      {
      operation: 'generatedCode'
      }
    ], port: this.port})
  }
  /**
   * install the project's schema
   */
  async install(): Promise<Record<string, boolean>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'install'
      }
    ]

    const response: Awaited<Record<string, boolean>> = await this._compute()

    return response
  }
  /**
   * name of the project
   */
  async name(): Promise<Record<string, string>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'name'
      }
    ]

    const response: Awaited<Record<string, string>> = await this._compute()

    return response
  }
  /**
   * schema provided by the project
   */
  async schema(): Promise<Record<string, string>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'schema'
      }
    ]

    const response: Awaited<Record<string, string>> = await this._compute()

    return response
  }
  /**
   * sdk used to generate code for and/or execute this project
   */
  async sdk(): Promise<Record<string, string>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'sdk'
      }
    ]

    const response: Awaited<Record<string, string>> = await this._compute()

    return response
  }
}


export type ClientCacheVolumeArgs = {
  key: string;
};

export type ClientContainerArgs = {
  id?: ContainerID;
  platform?: Platform;
};

export type ClientDirectoryArgs = {
  id?: DirectoryID;
};

export type ClientFileArgs = {
  id: FileID;
};

export type ClientGitArgs = {
  url: string;
  keepGitDir?: boolean;
};

export type ClientHttpArgs = {
  url: string;
};

export type ClientProjectArgs = {
  name: string;
};

export type ClientSecretArgs = {
  id: SecretID;
};


export default class Client extends BaseClient {
  /**
   * Construct a cache volume for a given cache key
   */
  cacheVolume(args: ClientCacheVolumeArgs): CacheVolume {
    return new CacheVolume({queryTree: [
      ...this._queryTree,
      {
      operation: 'cacheVolume',
      args
      }
    ], port: this.port})
  }
  /**
   * Load a container from ID.
   * Null ID returns an empty container (scratch).
   * Optional platform argument initializes new containers to execute and publish as that platform. Platform defaults to that of the builder's host.
   */
  container(args?: ClientContainerArgs): Container {
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'container',
      args
      }
    ], port: this.port})
  }
  /**
   * The default platform of the builder.
   */
  async defaultPlatform(): Promise<Record<string, Platform>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'defaultPlatform'
      }
    ]

    const response: Awaited<Record<string, Platform>> = await this._compute()

    return response
  }
  /**
   * Load a directory by ID. No argument produces an empty directory.
   */
  directory(args?: ClientDirectoryArgs): Directory {
    return new Directory({queryTree: [
      ...this._queryTree,
      {
      operation: 'directory',
      args
      }
    ], port: this.port})
  }
  /**
   * Load a file by ID
   */
  file(args: ClientFileArgs): File {
    return new File({queryTree: [
      ...this._queryTree,
      {
      operation: 'file',
      args
      }
    ], port: this.port})
  }
  /**
   * Query a git repository
   */
  git(args: ClientGitArgs): GitRepository {
    return new GitRepository({queryTree: [
      ...this._queryTree,
      {
      operation: 'git',
      args
      }
    ], port: this.port})
  }
  /**
   * Query the host environment
   */
  host(): Host {
    return new Host({queryTree: [
      ...this._queryTree,
      {
      operation: 'host'
      }
    ], port: this.port})
  }
  /**
   * An http remote
   */
  http(args: ClientHttpArgs): File {
    return new File({queryTree: [
      ...this._queryTree,
      {
      operation: 'http',
      args
      }
    ], port: this.port})
  }
  /**
   * Look up a project by name
   */
  project(args: ClientProjectArgs): Project {
    return new Project({queryTree: [
      ...this._queryTree,
      {
      operation: 'project',
      args
      }
    ], port: this.port})
  }
  /**
   * Load a secret from its ID
   */
  secret(args: ClientSecretArgs): Secret {
    return new Secret({queryTree: [
      ...this._queryTree,
      {
      operation: 'secret',
      args
      }
    ], port: this.port})
  }
}


/**
 * A reference to a secret value, which can be handled more safely than the value itself
 */
class Secret extends BaseClient {
  /**
   * The identifier for this secret
   */
  async id(): Promise<Record<string, SecretID>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'id'
      }
    ]

    const response: Awaited<Record<string, SecretID>> = await this._compute()

    return response
  }
  /**
   * The value of this secret
   */
  async plaintext(): Promise<Record<string, string>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'plaintext'
      }
    ]

    const response: Awaited<Record<string, string>> = await this._compute()

    return response
  }
}


/**
 * A unique identifier for a secret
 */
export type SecretID = any

























export type __TypeEnumValuesArgs = {
  includeDeprecated?: boolean;
};

export type __TypeFieldsArgs = {
  includeDeprecated?: boolean;
};





