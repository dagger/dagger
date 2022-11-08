import { GraphQLClient, gql } from "../index.js";
import { 
  ContainerExecArgs, 
  ContainerWithFsArgs, 
  ContainerWithMountedDirectoryArgs, 
  ContainerWithSecretVariableArgs, 
  ContainerWithWorkdirArgs, 
  DirectoryEntriesArgs, 
  DirectoryFileArgs, 
  GitRepositoryBranchArgs, 
  HostEnvVariableArgs, 
  HostWorkdirArgs, 
  QueryContainerArgs, 
  QueryGitArgs, 
  Scalars,
  SecretId} from "./types.js";
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

export class Client extends BaseClient {
  
  /**
   * Load a container from ID. Null ID returns an empty container (scratch).
   */
  container(args?: QueryContainerArgs): Container {
    this._queryTree = [
      {
      operation: 'container',
      args
      }
    ]

    return new Container({queryTree: this._queryTree, port: this.port})
  }
  
  /**
   * Construct a cache volume for a given cache key
   */
  cacheVolume(args: {key: Scalars['String']}): CacheVolume {
    this._queryTree = [
      {
      operation: 'cacheVolume',
      args
      }
    ]

    return new CacheVolume({queryTree: this._queryTree, port: this.port})
  }

  /**
   * Query a git repository
   */
  git(args: QueryGitArgs): Git {
    this._queryTree = [
      {
      operation: 'git',
      args
      }
    ]

    return new Git({queryTree: this._queryTree, port: this.port})
  }

  /**
   * Query the host environment
   */
  host(): Host {
    this._queryTree = [
      {
      operation: 'host',
      }
    ]

    return new Host({queryTree: this._queryTree, port: this.port})
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

  envVariable(args?: HostEnvVariableArgs): HostVariable {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'envVariable',
      args
      }
    ]

    return new HostVariable({queryTree: this._queryTree, port: this.port})
  }
  /**
   * The current working directory on the host
   */
  workdir(args?: HostWorkdirArgs): Directory {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'workdir',
      args
      }
    ]

    return new Directory({queryTree: this._queryTree, port: this.port})
  }
}

class HostVariable extends BaseClient {
  secret(): Secret {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: 'secret'
      }
    ]

    return new Secret({queryTree: this._queryTree, port: this.port})
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
}

class Git extends BaseClient {
  /**
   * Details on one branch
   */
  branch(args: GitRepositoryBranchArgs): Tree {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: 'branch',
        args
      }
    ]

    return new Tree({queryTree: this._queryTree, port: this.port})
  }

}
class Tree extends BaseClient {
  /**
   * The filesystem tree at this ref
   */
  tree(): Directory {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: 'tree'
      }
    ]

    return new Directory({queryTree: this._queryTree, port: this.port})
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
  exec(args: ContainerExecArgs): Container {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'exec',
      args
      }
    ]
    
    return new Container({queryTree: this._queryTree, port: this.port})
  }

  /**
   * Initialize this container from the base image published at the given address
   */
  from(args: {address: Scalars['String']} ): Container {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'from',
      args
      }
    ]
    
    return new Container({queryTree: this._queryTree, port: this.port})
  }

  /**
   * This container's root filesystem. Mounts are not included.
   */
  fs(): Directory {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'fs',
      }
    ]
    
    return new Directory({queryTree: this._queryTree, port: this.port})
  }

  /**
   * List of paths where a directory is mounted
   */
  async mounts(): Promise<Record<string, Array<Scalars['String']>>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'mounts',
      }
    ]

    const response: Awaited<Record<string, Array<Scalars['String']>>> = await this._compute()

    return response
  }

/**
 * Initialize this container from this DirectoryID
 */
  withFS(args: ContainerWithFsArgs): Container {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'withFS',
      args
      }
    ]
    
    return new Container({queryTree: this._queryTree, port: this.port})
  }

  /**
   * This container plus a directory mounted at the given path
   */
  withMountedDirectory(args: ContainerWithMountedDirectoryArgs): Container {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'withMountedDirectory',
      args
      }
    ]
    
    return new Container({queryTree: this._queryTree, port: this.port})
  }

  /**
   * This container plus a cache volume mounted at the given path
   */
  withMountedCache(args: {
    path: Scalars['String'];
    cache: Scalars['CacheID'];
    source?: Scalars['DirectoryID'];
  }): Container {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'withMountedCache',
      args
      }
    ]
    
    return new Container({queryTree: this._queryTree, port: this.port})
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
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'stdout',
      }
    ]
    
    return new File({queryTree: this._queryTree, port: this.port})
  }

/**
 * This container but with a different working directory
 */
  withWorkdir(args: ContainerWithWorkdirArgs): Container {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'withWorkdir',
      args
      }
    ]

    return new Container({queryTree: this._queryTree, port: this.port})
  }

  withSecretVariable(args: ContainerWithSecretVariableArgs): Container {
  
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'withSecretVariable',
      args
      }
    ]

    return new Container({queryTree: this._queryTree, port: this.port})
  }

  /**
   * This container plus the given environment variable
   */
  withEnvVariable(args: { name: Scalars['String'], value: Scalars['String']}): Container {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'withEnvVariable',
      args
      }
    ]

    return new Container({queryTree: this._queryTree, port: this.port})
  }

  /**
   * Retrieve a directory at the given path. Mounts are included.
   */
  directory(args: {path: Scalars['String'];}): Directory {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'directory',
      args
      }
    ]

    return new Directory({queryTree: this._queryTree, port: this.port})
  }
}

class Directory extends BaseClient {

  /**
   * Retrieve a file at the given path
   */
  file(args: DirectoryFileArgs): File {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: 'file',
        args
      }
    ]

    return new File({queryTree: this._queryTree, port: this.port}) 
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
  async entries(args?: DirectoryEntriesArgs): Promise<Record<string, Array<Scalars['String']>>>  {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'entries',
      args
      }
    ]

    const response: Record<string, Array<Scalars['String']>> = await this._compute()

    return response
  }
}