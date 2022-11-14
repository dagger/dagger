import { GraphQLClient, gql } from "../index.js";
import { Scalars, SecretId} from "./types.js";
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

    try {
    // run the query and return the result.
    const query = queryBuilder(this._queryTree)
    const response: Awaited<Promise<Record<string, any>>> = await this.client.request(gql`${query}`)
    const computeQuery = queryFlatten(response)
    const result = await computeQuery;

    return result      
    } catch (error) {
      console.error(`Failed: \n ${JSON.stringify(error, undefined, 2)}`)    
      process.exit(1)  
    }
  }
}

export default class Client extends BaseClient {
  
  /**
   * Load a container from ID. Null ID returns an empty container (scratch).
   */
  container(args?: {
    id?: any;
}): Container {

    return new Container({queryTree: [
      {
      operation: 'container',
      args
      }
    ], port: this.port})
  }
  
  /**
   * Construct a cache volume for a given cache key
   */
  cacheVolume(args: {key: string}): CacheVolume {

    return new CacheVolume({queryTree: [
      {
      operation: 'cacheVolume',
      args
      }
    ], port: this.port})
  }

  /**
   * Query a git repository
   */
  git(args: {
    url: string;
  }): Git {

    return new Git({queryTree: [
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
      {
      operation: 'host',
      }
    ], port: this.port})
  }

  secret(args: {
    id: Scalars['SecretID'];
}): Secret {

    return new Secret({queryTree: [
      {
      operation: 'secret',
      args
      }
    ], port: this.port})
  }

}

class CacheVolume extends BaseClient {
  /**
   * A unique identifier for this container
   */
  async id(): Promise<Record<string, Scalars['CacheID']>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'id',
      }
    ]

    const response: Awaited<Record<string, Scalars['CacheID']>> = await this._compute()

    return response
  }
}

class Host extends BaseClient {

  envVariable(args?: {
    name: string;
}): HostVariable {

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
  workdir(args?: {
    exclude?: string[] | undefined,
    include?: string[] | undefined
}): Directory {

    return new Directory({queryTree: [
      ...this._queryTree,
      {
      operation: 'workdir',
      args
      }
    ], port: this.port})
  }
}

class HostVariable extends BaseClient {
  secret(): Secret {

    return new Secret({queryTree: [
      ...this._queryTree,
      {
        operation: 'secret'
      }
    ], port: this.port})
  }
  
  async value(): Promise<Record<string, string>> {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: 'value'
      }
    ]

    const response: Record<string, string> = await this._compute()

    return response
  }
}

class Secret extends BaseClient {
  async id(): Promise<Record<string, SecretId>> {
    this._queryTree = [
      ...this._queryTree, 
      {
      operation: 'id'
      }
    ]

    const response: Record<string, SecretId> = await this._compute()

    return response
  }

  async plaintext(): Promise<Record<string, string>> {
    this._queryTree = [
      ...this._queryTree, 
      {
      operation: 'plaintext'
      }
    ]

    const response: Record<string, string> = await this._compute()

    return response
  }
}

class Git extends BaseClient {
  /**
   * Details on one branch
   */
  branch(args: {
    name: string;
}): Tree {

    return new Tree({queryTree: [
      ...this._queryTree,
      {
        operation: 'branch',
        args
      }
    ], port: this.port})
  }

}
class Tree extends BaseClient {
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

    const response: Record<string, string> = await this._compute()

    return response
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

    const response: Record<string, number> = await this._compute()

    return response
  }
}

class Container extends BaseClient {

  /**
   * This container after executing the specified command inside it
   */
  exec(args: {
    args?: string[] | undefined,
    stdin?: string | undefined,
    redirectStdout?: string | undefined,
    redirectStderr?: string | undefined,
  }): Container {
    
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'exec',
      args
      }
    ], port: this.port})
  }

  /**
   * Initialize this container from the base image published at the given address
   */
  from(args: {address: string} ): Container {
    
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
      operation: 'fs',
      }
    ], port: this.port})
  }

  /**
   * List of paths where a directory is mounted
   */
  async mounts(): Promise<Record<string, Array<string>>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'mounts',
      }
    ]

    const response: Awaited<Record<string, Array<string>>> = await this._compute()

    return response
  }

  async exitCode(): Promise<Record<string, Scalars['Int']>> {
  this._queryTree = [
      ...this._queryTree,
      {
      operation: 'exitCode',
      }
    ]

    const response: Awaited<Record<string, Scalars['Int']>> = await this._compute()

    return response
  }

/**
 * Initialize this container from this DirectoryID
 */
  withFS(args: {
    id: Scalars['DirectoryID'];
}): Container {
    
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withFS',
      args
      }
    ], port: this.port})
  }

  /**
   * This container plus a directory mounted at the given path
   */
  withMountedDirectory(args: {
    path: string;
    source: Scalars['DirectoryID'];
}): Container {
    
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withMountedDirectory',
      args
      }
    ], port: this.port})
  }

  /**
   * This container plus a cache volume mounted at the given path
   */
  withMountedCache(args: {
    path: string;
    cache: Scalars['CacheID'];
    source?: Scalars['DirectoryID'];
  }): Container {
    
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withMountedCache',
      args
      }
    ], port: this.port})
  }

  /**
   * A unique identifier for this container
   */
  async id(): Promise<Record<string, Scalars['ContainerID']>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'id',
      }
    ]

    const response: Awaited<Record<string, Scalars['ContainerID']>> = await this._compute()

    return response
  }

/**
 * The output stream of the last executed command. Null if no command has been executed.
 */
  stdout(): File {
    
    return new File({queryTree: [
      ...this._queryTree,
      {
      operation: 'stdout',
      }
    ], port: this.port})
  }

/**
 * The error stream of the last executed command. Null if no command has been executed.
 */
  stderr(): File {
    
    return new File({queryTree: [
      ...this._queryTree,
      {
      operation: 'stderr',
      }
    ], port: this.port})
  }

/**
 * This container but with a different working directory
 */
  withWorkdir(args: {
    path: string;
}): Container {

    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withWorkdir',
      args
      }
    ], port: this.port})
  }

  /**
   * This container plus an env variable containing the given secret
   * @arg name: string
   * @arg secret: string
   */
  withSecretVariable(args: {
    name: string;
    secret: any;
}): Container {
  
    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withSecretVariable',
      args
      }
    ], port: this.port})
  }

  /**
   * This container plus the given environment variable
   */
  withEnvVariable(args: { name: string, value: string}): Container {

    return new Container({queryTree: [
      ...this._queryTree,
      {
      operation: 'withEnvVariable',
      args
      }
    ], port: this.port})
  }

  /**
   * Retrieve a directory at the given path. Mounts are included.
   */
  directory(args: {path: string}): Directory {

    return new Directory({queryTree: [
      ...this._queryTree,
      {
      operation: 'directory',
      args
      }
    ], port: this.port})
  }
}

class Directory extends BaseClient {

  /**
   * Retrieve a file at the given path
   */
  file(args: {
    path: string;
}): File {

    return new File({queryTree: [
      ...this._queryTree,
      {
        operation: 'file',
        args
      }
    ], port: this.port}) 
  }
  
  /**
   * Retrieve a directory at the given path. Mounts are included.
   */
  async id(): Promise<Record<string, Scalars['DirectoryID']>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'id'
      }
    ]

    const response: Record<string, Scalars['DirectoryID']> = await this._compute()

    return response
  }

  /**
   * Return a list of files and directories at the given path
   */
  async entries(args?: {
    path?: string | undefined;
}): Promise<Record<string, Array<string>>>  {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'entries',
      args
      }
    ]

    const response: Record<string, Array<string>> = await this._compute()

    return response
  }

  async export(args?: {
    path?: string | undefined;
}): Promise<Record<string, Array<boolean>>>  {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'export',
      args
      }
    ]

    const response: Record<string, Array<boolean>> = await this._compute()

    return response
  }
}