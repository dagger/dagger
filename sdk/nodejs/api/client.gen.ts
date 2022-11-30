/**
 * This file was auto-generated by `cloak clientgen`.
 * Do not make direct changes to the file.
 */

import { GraphQLClient } from "graphql-request"
import { queryBuilder } from "./utils.js"

/**
 * @hidden
 */
export type QueryTree = {
  operation: string
  args?: Record<string, unknown>
}

interface ClientConfig {
  queryTree?: QueryTree[]
  host?: string
}

class BaseClient {
  protected _queryTree: QueryTree[]
  protected client: GraphQLClient
  /**
   * @defaultValue `127.0.0.1:8080`
   */
  public clientHost: string

  /**
   * @hidden
   */
  constructor({ queryTree, host }: ClientConfig = {}) {
    this._queryTree = queryTree || []
    this.clientHost = host || "127.0.0.1:8080"
    this.client = new GraphQLClient(`http://${host}/query`)
  }

  /**
   * @hidden
   */
  get queryTree() {
    return this._queryTree
  }
}

/**
 * A global cache volume identifier
 */
export type CacheID = string

export type ContainerBuildOpts = {
  dockerfile?: string
}

export type ContainerExecOpts = {
  args?: string[]
  stdin?: string
  redirectStdout?: string
  redirectStderr?: string
  experimentalPrivilegedNesting?: boolean
}

export type ContainerExportOpts = {
  platformVariants?: ContainerID[] | Container[]
}

export type ContainerPublishOpts = {
  platformVariants?: ContainerID[] | Container[]
}

export type ContainerWithDefaultArgsOpts = {
  args?: string[]
}

export type ContainerWithDirectoryOpts = {
  exclude?: string[]
  include?: string[]
}

export type ContainerWithExecOpts = {
  stdin?: string
  redirectStdout?: string
  redirectStderr?: string
  experimentalPrivilegedNesting?: boolean
}

export type ContainerWithMountedCacheOpts = {
  source?: DirectoryID | Directory
}

export type ContainerWithNewFileOpts = {
  contents?: string
}

/**
 * A unique container identifier. Null designates an empty container (scratch).
 */
export type ContainerID = string

/**
 * The `DateTime` scalar type represents a DateTime. The DateTime is serialized as an RFC 3339 quoted string
 */
export type DateTime = string

export type DirectoryDockerBuildOpts = {
  dockerfile?: string
  platform?: Platform
}

export type DirectoryEntriesOpts = {
  path?: string
}

export type DirectoryWithDirectoryOpts = {
  exclude?: string[]
  include?: string[]
}

/**
 * A content-addressed directory identifier
 */
export type DirectoryID = string

export type FileID = string

export type GitRefTreeOpts = {
  sshKnownHosts?: string
  sshAuthSocket?: SocketID
}

export type HostDirectoryOpts = {
  exclude?: string[]
  include?: string[]
}

export type HostWorkdirOpts = {
  exclude?: string[]
  include?: string[]
}

/**
 * The `ID` scalar type represents a unique identifier, often used to refetch an object or as key for a cache. The ID type appears in a JSON response as a String; however, it is not intended to be human-readable. When expected as an input type, any string (such as `"4"`) or integer (such as `4`) input value will be accepted as an ID.
 */
export type ID = string

export type Platform = string

export type ClientContainerOpts = {
  id?: ContainerID | Container
  platform?: Platform
}

export type ClientDirectoryOpts = {
  id?: DirectoryID | Directory
}

export type ClientGitOpts = {
  keepGitDir?: boolean
}

export type ClientSocketOpts = {
  id?: SocketID
}

/**
 * A unique identifier for a secret
 */
export type SecretID = string

/**
 * A content-addressed socket identifier
 */
export type SocketID = string

export type __TypeEnumValuesOpts = {
  includeDeprecated?: boolean
}

export type __TypeFieldsOpts = {
  includeDeprecated?: boolean
}

/**
 * A directory whose contents persist across runs
 */
export class CacheVolume extends BaseClient {
  async id(): Promise<CacheID> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "id",
      },
    ]

    const response: Awaited<CacheID> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }
}

/**
 * An OCI-compatible container, also known as a docker container
 */
export class Container extends BaseClient {
  /**
   * Initialize this container from a Dockerfile build
   */
  build(
    context: DirectoryID | Directory,
    opts?: ContainerBuildOpts
  ): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "build",
          args: { context, ...opts },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Default arguments for future commands
   */
  async defaultArgs(): Promise<string[]> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "defaultArgs",
      },
    ]

    const response: Awaited<string[]> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * Retrieve a directory at the given path. Mounts are included.
   */
  directory(path: string): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "directory",
          args: { path },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Entrypoint to be prepended to the arguments of all commands
   */
  async entrypoint(): Promise<string[]> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "entrypoint",
      },
    ]

    const response: Awaited<string[]> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * The value of the specified environment variable
   */
  async envVariable(name: string): Promise<string> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "envVariable",
        args: { name },
      },
    ]

    const response: Awaited<string> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * A list of environment variables passed to commands
   */
  async envVariables(): Promise<EnvVariable[]> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "envVariables",
      },
    ]

    const response: Awaited<EnvVariable[]> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * This container after executing the specified command inside it
   *
   * @param opts optional params for exec
   *
   * @deprecated Replaced by withExec.
   */
  exec(opts?: ContainerExecOpts): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "exec",
          args: { ...opts },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Exit code of the last executed command. Zero means success.
   * Null if no command has been executed.
   */
  async exitCode(): Promise<number> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "exitCode",
      },
    ]

    const response: Awaited<number> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * Write the container as an OCI tarball to the destination file path on the host
   */
  async export(path: string, opts?: ContainerExportOpts): Promise<boolean> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "export",
        args: { path, ...opts },
      },
    ]

    const response: Awaited<boolean> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * Retrieve a file at the given path. Mounts are included.
   */
  file(path: string): File {
    return new File({
      queryTree: [
        ...this._queryTree,
        {
          operation: "file",
          args: { path },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Initialize this container from the base image published at the given address
   */
  from(address: string): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "from",
          args: { address },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container's root filesystem. Mounts are not included.
   *
   *
   * @deprecated Replaced by rootfs.
   */
  fs(): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "fs",
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * A unique identifier for this container
   */
  async id(): Promise<ContainerID> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "id",
      },
    ]

    const response: Awaited<ContainerID> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * List of paths where a directory is mounted
   */
  async mounts(): Promise<string[]> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "mounts",
      },
    ]

    const response: Awaited<string[]> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * The platform this container executes and publishes as
   */
  async platform(): Promise<Platform> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "platform",
      },
    ]

    const response: Awaited<Platform> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * Publish this container as a new image, returning a fully qualified ref
   */
  async publish(address: string, opts?: ContainerPublishOpts): Promise<string> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "publish",
        args: { address, ...opts },
      },
    ]

    const response: Awaited<string> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * This container's root filesystem. Mounts are not included.
   */
  rootfs(): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "rootfs",
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * The error stream of the last executed command.
   * Null if no command has been executed.
   */
  async stderr(): Promise<string> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "stderr",
      },
    ]

    const response: Awaited<string> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * The output stream of the last executed command.
   * Null if no command has been executed.
   */
  async stdout(): Promise<string> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "stdout",
      },
    ]

    const response: Awaited<string> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * The user to be set for all commands
   */
  async user(): Promise<string> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "user",
      },
    ]

    const response: Awaited<string> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * Configures default arguments for future commands
   */
  withDefaultArgs(opts?: ContainerWithDefaultArgsOpts): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withDefaultArgs",
          args: { ...opts },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container plus a directory written at the given path
   */
  withDirectory(
    path: string,
    directory: DirectoryID | Directory,
    opts?: ContainerWithDirectoryOpts
  ): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withDirectory",
          args: { path, directory, ...opts },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container but with a different command entrypoint
   */
  withEntrypoint(args: string[]): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withEntrypoint",
          args: { args },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container plus the given environment variable
   */
  withEnvVariable(name: string, value: string): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withEnvVariable",
          args: { name, value },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container after executing the specified command inside it
   */
  withExec(args: string[], opts?: ContainerWithExecOpts): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withExec",
          args: { args, ...opts },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Initialize this container from this DirectoryID
   *
   *
   * @deprecated Replaced by withRootfs.
   */
  withFS(id: DirectoryID | Directory): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withFS",
          args: { id },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container plus the contents of the given file copied to the given path
   */
  withFile(path: string, source: FileID | File): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withFile",
          args: { path, source },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container plus a cache volume mounted at the given path
   */
  withMountedCache(
    path: string,
    cache: CacheID | CacheVolume,
    opts?: ContainerWithMountedCacheOpts
  ): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withMountedCache",
          args: { path, cache, ...opts },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container plus a directory mounted at the given path
   */
  withMountedDirectory(
    path: string,
    source: DirectoryID | Directory
  ): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withMountedDirectory",
          args: { path, source },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container plus a file mounted at the given path
   */
  withMountedFile(path: string, source: FileID | File): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withMountedFile",
          args: { path, source },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container plus a secret mounted into a file at the given path
   */
  withMountedSecret(path: string, source: SecretID | Secret): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withMountedSecret",
          args: { path, source },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container plus a temporary directory mounted at the given path
   */
  withMountedTemp(path: string): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withMountedTemp",
          args: { path },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container plus a new file written at the given path
   */
  withNewFile(path: string, opts?: ContainerWithNewFileOpts): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withNewFile",
          args: { path, ...opts },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Initialize this container from this DirectoryID
   */
  withRootfs(id: DirectoryID | Directory): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withRootfs",
          args: { id },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container plus an env variable containing the given secret
   */
  withSecretVariable(name: string, secret: SecretID | Secret): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withSecretVariable",
          args: { name, secret },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container plus a socket forwarded to the given Unix socket path
   */
  withUnixSocket(path: string, source: SocketID): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withUnixSocket",
          args: { path, source },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container but with a different command user
   */
  withUser(name: string): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withUser",
          args: { name },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container but with a different working directory
   */
  withWorkdir(path: string): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withWorkdir",
          args: { path },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container minus the given environment variable
   */
  withoutEnvVariable(name: string): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withoutEnvVariable",
          args: { name },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container after unmounting everything at the given path.
   */
  withoutMount(path: string): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withoutMount",
          args: { path },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container with a previously added Unix socket removed
   */
  withoutUnixSocket(path: string): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withoutUnixSocket",
          args: { path },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * The working directory for all commands
   */
  async workdir(): Promise<string> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "workdir",
      },
    ]

    const response: Awaited<string> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }
}

/**
 * A directory
 */
export class Directory extends BaseClient {
  /**
   * The difference between this directory and an another directory
   */
  diff(other: DirectoryID | Directory): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "diff",
          args: { other },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Retrieve a directory at the given path
   */
  directory(path: string): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "directory",
          args: { path },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Build a new Docker container from this directory
   */
  dockerBuild(opts?: DirectoryDockerBuildOpts): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "dockerBuild",
          args: { ...opts },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Return a list of files and directories at the given path
   */
  async entries(opts?: DirectoryEntriesOpts): Promise<string[]> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "entries",
        args: { ...opts },
      },
    ]

    const response: Awaited<string[]> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * Write the contents of the directory to a path on the host
   */
  async export(path: string): Promise<boolean> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "export",
        args: { path },
      },
    ]

    const response: Awaited<boolean> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * Retrieve a file at the given path
   */
  file(path: string): File {
    return new File({
      queryTree: [
        ...this._queryTree,
        {
          operation: "file",
          args: { path },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * The content-addressed identifier of the directory
   */
  async id(): Promise<DirectoryID> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "id",
      },
    ]

    const response: Awaited<DirectoryID> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * load a project's metadata
   */
  loadProject(configPath: string): Project {
    return new Project({
      queryTree: [
        ...this._queryTree,
        {
          operation: "loadProject",
          args: { configPath },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This directory plus a directory written at the given path
   */
  withDirectory(
    path: string,
    directory: DirectoryID | Directory,
    opts?: DirectoryWithDirectoryOpts
  ): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withDirectory",
          args: { path, directory, ...opts },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This directory plus the contents of the given file copied to the given path
   */
  withFile(path: string, source: FileID | File): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withFile",
          args: { path, source },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This directory plus a new directory created at the given path
   */
  withNewDirectory(path: string): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withNewDirectory",
          args: { path },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This directory plus a new file written at the given path
   */
  withNewFile(path: string, contents: string): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withNewFile",
          args: { path, contents },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This directory with the directory at the given path removed
   */
  withoutDirectory(path: string): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withoutDirectory",
          args: { path },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This directory with the file at the given path removed
   */
  withoutFile(path: string): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withoutFile",
          args: { path },
        },
      ],
      host: this.clientHost,
    })
  }
}

/**
 * EnvVariable is a simple key value object that represents an environment variable.
 */
export class EnvVariable extends BaseClient {
  /**
   * name is the environment variable name.
   */
  async name(): Promise<string> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "name",
      },
    ]

    const response: Awaited<string> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * value is the environment variable value
   */
  async value(): Promise<string> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "value",
      },
    ]

    const response: Awaited<string> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }
}

/**
 * A file
 */
export class File extends BaseClient {
  /**
   * The contents of the file
   */
  async contents(): Promise<string> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "contents",
      },
    ]

    const response: Awaited<string> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * Write the file to a file path on the host
   */
  async export(path: string): Promise<boolean> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "export",
        args: { path },
      },
    ]

    const response: Awaited<boolean> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * The content-addressed identifier of the file
   */
  async id(): Promise<FileID> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "id",
      },
    ]

    const response: Awaited<FileID> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }
  secret(): Secret {
    return new Secret({
      queryTree: [
        ...this._queryTree,
        {
          operation: "secret",
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * The size of the file, in bytes
   */
  async size(): Promise<number> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "size",
      },
    ]

    const response: Awaited<number> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }
}

/**
 * A git ref (tag or branch)
 */
export class GitRef extends BaseClient {
  /**
   * The digest of the current value of this ref
   */
  async digest(): Promise<string> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "digest",
      },
    ]

    const response: Awaited<string> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * The filesystem tree at this ref
   */
  tree(opts?: GitRefTreeOpts): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "tree",
          args: { ...opts },
        },
      ],
      host: this.clientHost,
    })
  }
}

/**
 * A git repository
 */
export class GitRepository extends BaseClient {
  /**
   * Details on one branch
   */
  branch(name: string): GitRef {
    return new GitRef({
      queryTree: [
        ...this._queryTree,
        {
          operation: "branch",
          args: { name },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * List of branches on the repository
   */
  async branches(): Promise<string[]> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "branches",
      },
    ]

    const response: Awaited<string[]> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * Details on one commit
   */
  commit(id: string): GitRef {
    return new GitRef({
      queryTree: [
        ...this._queryTree,
        {
          operation: "commit",
          args: { id },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Details on one tag
   */
  tag(name: string): GitRef {
    return new GitRef({
      queryTree: [
        ...this._queryTree,
        {
          operation: "tag",
          args: { name },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * List of tags on the repository
   */
  async tags(): Promise<string[]> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "tags",
      },
    ]

    const response: Awaited<string[]> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }
}

/**
 * Information about the host execution environment
 */
export class Host extends BaseClient {
  /**
   * Access a directory on the host
   */
  directory(path: string, opts?: HostDirectoryOpts): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "directory",
          args: { path, ...opts },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Access an environment variable on the host
   */
  envVariable(name: string): HostVariable {
    return new HostVariable({
      queryTree: [
        ...this._queryTree,
        {
          operation: "envVariable",
          args: { name },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Access a Unix socket on the host
   */
  unixSocket(path: string): Socket {
    return new Socket({
      queryTree: [
        ...this._queryTree,
        {
          operation: "unixSocket",
          args: { path },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * The current working directory on the host
   *
   * @param opts optional params for workdir
   *
   * @deprecated Use directory with path set to '.' instead.
   */
  workdir(opts?: HostWorkdirOpts): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "workdir",
          args: { ...opts },
        },
      ],
      host: this.clientHost,
    })
  }
}

/**
 * An environment variable on the host environment
 */
export class HostVariable extends BaseClient {
  /**
   * A secret referencing the value of this variable
   */
  secret(): Secret {
    return new Secret({
      queryTree: [
        ...this._queryTree,
        {
          operation: "secret",
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * The value of this variable
   */
  async value(): Promise<string> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "value",
      },
    ]

    const response: Awaited<string> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }
}

/**
 * A set of scripts and/or extensions
 */
export class Project extends BaseClient {
  /**
   * extensions in this project
   */
  async extensions(): Promise<Project[]> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "extensions",
      },
    ]

    const response: Awaited<Project[]> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * Code files generated by the SDKs in the project
   */
  generatedCode(): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "generatedCode",
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * install the project's schema
   */
  async install(): Promise<boolean> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "install",
      },
    ]

    const response: Awaited<boolean> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * name of the project
   */
  async name(): Promise<string> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "name",
      },
    ]

    const response: Awaited<string> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * schema provided by the project
   */
  async schema(): Promise<string> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "schema",
      },
    ]

    const response: Awaited<string> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * sdk used to generate code for and/or execute this project
   */
  async sdk(): Promise<string> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "sdk",
      },
    ]

    const response: Awaited<string> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }
}

export default class Client extends BaseClient {
  /**
   * Construct a cache volume for a given cache key
   */
  cacheVolume(key: string): CacheVolume {
    return new CacheVolume({
      queryTree: [
        ...this._queryTree,
        {
          operation: "cacheVolume",
          args: { key },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Load a container from ID.
   * Null ID returns an empty container (scratch).
   * Optional platform argument initializes new containers to execute and publish as that platform. Platform defaults to that of the builder's host.
   */
  container(opts?: ClientContainerOpts): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "container",
          args: { ...opts },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * The default platform of the builder.
   */
  async defaultPlatform(): Promise<Platform> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "defaultPlatform",
      },
    ]

    const response: Awaited<Platform> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * Load a directory by ID. No argument produces an empty directory.
   */
  directory(opts?: ClientDirectoryOpts): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "directory",
          args: { ...opts },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Load a file by ID
   */
  file(id: FileID | File): File {
    return new File({
      queryTree: [
        ...this._queryTree,
        {
          operation: "file",
          args: { id },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Query a git repository
   */
  git(url: string, opts?: ClientGitOpts): GitRepository {
    return new GitRepository({
      queryTree: [
        ...this._queryTree,
        {
          operation: "git",
          args: { url, ...opts },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Query the host environment
   */
  host(): Host {
    return new Host({
      queryTree: [
        ...this._queryTree,
        {
          operation: "host",
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * An http remote
   */
  http(url: string): File {
    return new File({
      queryTree: [
        ...this._queryTree,
        {
          operation: "http",
          args: { url },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Look up a project by name
   */
  project(name: string): Project {
    return new Project({
      queryTree: [
        ...this._queryTree,
        {
          operation: "project",
          args: { name },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Load a secret from its ID
   */
  secret(id: SecretID | Secret): Secret {
    return new Secret({
      queryTree: [
        ...this._queryTree,
        {
          operation: "secret",
          args: { id },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Load a socket by ID
   */
  socket(opts?: ClientSocketOpts): Socket {
    return new Socket({
      queryTree: [
        ...this._queryTree,
        {
          operation: "socket",
          args: { ...opts },
        },
      ],
      host: this.clientHost,
    })
  }
}

/**
 * A reference to a secret value, which can be handled more safely than the value itself
 */
export class Secret extends BaseClient {
  /**
   * The identifier for this secret
   */
  async id(): Promise<SecretID> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "id",
      },
    ]

    const response: Awaited<SecretID> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }

  /**
   * The value of this secret
   */
  async plaintext(): Promise<string> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "plaintext",
      },
    ]

    const response: Awaited<string> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }
}

export class Socket extends BaseClient {
  /**
   * The content-addressed identifier of the socket
   */
  async id(): Promise<SocketID> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "id",
      },
    ]

    const response: Awaited<SocketID> = await queryBuilder(
      this._queryTree,
      this.client
    )

    return response
  }
}
