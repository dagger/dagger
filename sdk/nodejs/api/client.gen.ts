/**
 * This file was auto-generated by `cloak clientgen`.
 * Do not make direct changes to the file.
 */

import axios from "axios"
import { GraphQLClient, gql } from "graphql-request"
import { queryBuilder, queryFlatten } from "./utils.js"
import { Headers, Response } from "cross-fetch"

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
  private client: GraphQLClient
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
    const url = `http://dagger/query`
    this.client = new GraphQLClient(url, {
      fetch: function (
        input: RequestInfo,
        init?: RequestInit
      ): Promise<Response> {
        return axios({
          method: init?.method,
          url: url,
          headers: init?.headers as Record<string, string>,
          data: init?.body,
          socketPath: host,
          responseType: "stream",
        }).then((res) => {
          const headers = new Headers()
          for (const [key, value] of Object.entries(res.headers)) {
            headers.append(key, value as string)
          }
          return new Response(res.data, {
            status: res.status,
            statusText: res.statusText,
            headers: headers,
          })
        })
      },
    })
  }

  /**
   * @hidden
   */
  get queryTree() {
    return this._queryTree
  }

  /**
   * @hidden
   */
  protected async _compute<T>(): Promise<T> {
    try {
      // run the query and return the result.
      const query = queryBuilder(this._queryTree)
      const computeQuery: Awaited<T> = await this.client.request(
        gql`
          ${query}
        `
      )

      return queryFlatten(computeQuery)
    } catch (error) {
      throw Error(`Error: ${JSON.stringify(error, undefined, 2)}`)
    }
  }
}

/**
 * A global cache volume identifier
 * @hidden
 */
export type CacheID = string

/**
 * A unique container identifier. Null designates an empty container (scratch).
 * @hidden
 */
export type ContainerID = string

/**
 * The `DateTime` scalar type represents a DateTime. The DateTime is serialized as an RFC 3339 quoted string
 * @hidden
 */
export type DateTime = string

/**
 * A content-addressed directory identifier
 * @hidden
 */
export type DirectoryID = string

export type FileID = string

/**
 * The `ID` scalar type represents a unique identifier, often used to refetch an object or as key for a cache. The ID type appears in a JSON response as a String; however, it is not intended to be human-readable. When expected as an input type, any string (such as `"4"`) or integer (such as `4`) input value will be accepted as an ID.
 * @hidden
 */
export type ID = string

export type Platform = string

/**
 * A unique identifier for a secret
 * @hidden
 */
export type SecretID = string

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

    const response: Awaited<CacheID> = await this._compute()

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
  build(context: DirectoryID, dockerfile?: string): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "build",
          args: { context, dockerfile },
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

    const response: Awaited<string[]> = await this._compute()

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

    const response: Awaited<string[]> = await this._compute()

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

    const response: Awaited<string> = await this._compute()

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

    const response: Awaited<EnvVariable[]> = await this._compute()

    return response
  }

  /**
   * This container after executing the specified command inside it
   *
   * @deprecated Replaced by withExec.
   */
  exec(
    args?: string[],
    stdin?: string,
    redirectStdout?: string,
    redirectStderr?: string,
    experimentalPrivilegedNesting?: boolean
  ): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "exec",
          args: {
            args,
            stdin,
            redirectStdout,
            redirectStderr,
            experimentalPrivilegedNesting,
          },
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

    const response: Awaited<number> = await this._compute()

    return response
  }

  /**
   * Write the container as an OCI tarball to the destination file path on the host
   */
  async export(
    path: string,
    platformVariants?: ContainerID[]
  ): Promise<boolean> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "export",
        args: { path, platformVariants },
      },
    ]

    const response: Awaited<boolean> = await this._compute()

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

    const response: Awaited<ContainerID> = await this._compute()

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

    const response: Awaited<string[]> = await this._compute()

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

    const response: Awaited<Platform> = await this._compute()

    return response
  }

  /**
   * Publish this container as a new image, returning a fully qualified ref
   */
  async publish(
    address: string,
    platformVariants?: ContainerID[]
  ): Promise<string> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "publish",
        args: { address, platformVariants },
      },
    ]

    const response: Awaited<string> = await this._compute()

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

    const response: Awaited<string> = await this._compute()

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

    const response: Awaited<string> = await this._compute()

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

    const response: Awaited<string> = await this._compute()

    return response
  }

  /**
   * Configures default arguments for future commands
   */
  withDefaultArgs(args?: string[]): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withDefaultArgs",
          args: { args },
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
  withExec(
    args: string[],
    stdin?: string,
    redirectStdout?: string,
    redirectStderr?: string,
    experimentalPrivilegedNesting?: boolean
  ): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withExec",
          args: {
            args,
            stdin,
            redirectStdout,
            redirectStderr,
            experimentalPrivilegedNesting,
          },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Initialize this container from this DirectoryID
   *
   * @deprecated Replaced by withRootfs.
   */
  withFS(id: DirectoryID): Container {
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
   * This container plus a cache volume mounted at the given path
   */
  withMountedCache(
    path: string,
    cache: CacheID,
    source?: DirectoryID
  ): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withMountedCache",
          args: { path, cache, source },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This container plus a directory mounted at the given path
   */
  withMountedDirectory(path: string, source: DirectoryID): Container {
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
  withMountedFile(path: string, source: FileID): Container {
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
  withMountedSecret(path: string, source: SecretID): Container {
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
   * Initialize this container from this DirectoryID
   */
  withRootfs(id: DirectoryID): Container {
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
  withSecretVariable(name: string, secret: SecretID): Container {
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
   * The working directory for all commands
   */
  async workdir(): Promise<string> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "workdir",
      },
    ]

    const response: Awaited<string> = await this._compute()

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
  diff(other: DirectoryID): Directory {
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
   * Return a list of files and directories at the given path
   */
  async entries(path?: string): Promise<string[]> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: "entries",
        args: { path },
      },
    ]

    const response: Awaited<string[]> = await this._compute()

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

    const response: Awaited<boolean> = await this._compute()

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

    const response: Awaited<DirectoryID> = await this._compute()

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
    directory: DirectoryID,
    exclude?: string[],
    include?: string[]
  ): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "withDirectory",
          args: { path, directory, exclude, include },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * This directory plus the contents of the given file copied to the given path
   */
  withFile(path: string, source: FileID): Directory {
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

    const response: Awaited<string> = await this._compute()

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

    const response: Awaited<string> = await this._compute()

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

    const response: Awaited<string> = await this._compute()

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

    const response: Awaited<boolean> = await this._compute()

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

    const response: Awaited<FileID> = await this._compute()

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

    const response: Awaited<number> = await this._compute()

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

    const response: Awaited<string> = await this._compute()

    return response
  }

  /**
   * The filesystem tree at this ref
   */
  tree(): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "tree",
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

    const response: Awaited<string[]> = await this._compute()

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

    const response: Awaited<string[]> = await this._compute()

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
  directory(path: string, exclude?: string[], include?: string[]): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "directory",
          args: { path, exclude, include },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Lookup the value of an environment variable. Null if the variable is not available.
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
   * The current working directory on the host
   *
   * @deprecated Use directory with path set to '.' instead.
   */
  workdir(exclude?: string[], include?: string[]): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "workdir",
          args: { exclude, include },
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

    const response: Awaited<string> = await this._compute()

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

    const response: Awaited<Project[]> = await this._compute()

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

    const response: Awaited<boolean> = await this._compute()

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

    const response: Awaited<string> = await this._compute()

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

    const response: Awaited<string> = await this._compute()

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

    const response: Awaited<string> = await this._compute()

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
  container(id?: ContainerID, platform?: Platform): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "container",
          args: { id, platform },
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

    const response: Awaited<Platform> = await this._compute()

    return response
  }

  /**
   * Load a directory by ID. No argument produces an empty directory.
   */
  directory(id?: DirectoryID): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "directory",
          args: { id },
        },
      ],
      host: this.clientHost,
    })
  }

  /**
   * Load a file by ID
   */
  file(id: FileID): File {
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
  git(url: string, keepGitDir?: boolean): GitRepository {
    return new GitRepository({
      queryTree: [
        ...this._queryTree,
        {
          operation: "git",
          args: { url, keepGitDir },
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
  secret(id: SecretID): Secret {
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

    const response: Awaited<SecretID> = await this._compute()

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

    const response: Awaited<string> = await this._compute()

    return response
  }
}
