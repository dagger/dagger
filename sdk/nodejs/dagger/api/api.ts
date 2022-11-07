import { GraphQLClient, gql } from "../index.js";
import { 
  ContainerExecArgs, 
  ContainerWithFsArgs, 
  ContainerWithMountedDirectoryArgs, 
  ContainerWithWorkdirArgs, 
  DirectoryEntriesArgs, 
  DirectoryFileArgs, 
  GitRepositoryBranchArgs, 
  HostWorkdirArgs, 
  QueryContainerArgs, 
  QueryGitArgs, 
  Scalars } from "./types.js";
import { queryBuilder, queryFlatten } from "./utils.js"

export type QueryTree = {
  operation: string
  args?: Record<string, any>
}

export class BaseClient {
  protected _queryTree:  QueryTree[]
  

  constructor(queryTree: QueryTree[] = []) {
    this._queryTree = queryTree
  }

  get queryTree() {
    return this._queryTree;
  }

  protected async _compute() : Promise<Record<string, any>> {
    // run the query and return the result.
    const query = queryBuilder(this._queryTree)

    const graphqlClient = new GraphQLClient("http://localhost:8080/query")

    const computeQuery: Promise<Record<string, string>> = new Promise(async (resolve) => {
      const response: Awaited<Promise<Record<string, any>>> = await graphqlClient.request(gql`${query}`)

      resolve(queryFlatten(response));
    })

    const result = await computeQuery;

    return result
  }
}

export default class Client extends BaseClient {
  
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

    return new Container(this._queryTree)
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

    return new CacheVolume(this._queryTree)
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

    return new Git(this._queryTree)
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

    return new Host(this._queryTree)
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

    return new Directory(this._queryTree)
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

    return new Tree(this._queryTree)
  }

}
class Tree extends BaseClient {
  /**
   * The filesystem tree at this ref
   */
  tree(): File {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: 'tree'
      }
    ]

    return new File(this._queryTree)
  }
}
class File extends BaseClient {
  /**
   * Retrieve a file at the given path
   */
  file(args: DirectoryFileArgs): Contents {
    this._queryTree = [
      ...this._queryTree,
      {
        operation: 'file',
        args
      }
    ]

    return new Contents(this._queryTree) 
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
    
    return new Container(this._queryTree)
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
    
    return new Container(this._queryTree)
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
    
    return new Directory(this._queryTree)
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
    
    return new Container(this._queryTree)
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
    
    return new Container(this._queryTree)
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
    
    return new Container(this._queryTree)
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
  stdout(): Contents {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'stdout',
      }
    ]
    
    return new Contents(this._queryTree)
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

    return new Container(this._queryTree)
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

    return new Container(this._queryTree)
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

    return new Directory(this._queryTree)
  }
}

class Directory extends BaseClient {
  
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

class Contents extends BaseClient {
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
}