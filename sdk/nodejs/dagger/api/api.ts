import { Engine, GraphQLClient, gql } from "../index.js";
import { 
  ContainerDirectoryArgs, 
  ContainerExecArgs, 
  ContainerFromArgs, 
  ContainerWithEnvVariableArgs, 
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

export class BaseApi {
  protected _queryTree:  QueryTree[]
  
  constructor(queryTree: QueryTree[]= []) {
    this._queryTree = queryTree
  }

  get queryTree() {
    return this._queryTree;
  }

  protected async _compute() : Promise<Record<string, any>> {
    // run the query and return the result.
    const query = queryBuilder(this._queryTree)
    
    const computeQuery: Promise<Record<string, string>> = new Promise(resolve  => 
      new Engine({}).run(async (client: GraphQLClient) => {
        const response: Awaited<Promise<Record<string, any>>> = await client.request(gql`${query}`)

        resolve(queryFlatten(response));
      })
    )

    const result = await computeQuery;

    return result
  }
}

export default class Api extends BaseApi {
  
  /**
   * Load a container from ID. Null ID returns an empty container (scratch).
   */
  container(args?: QueryContainerArgs): Container {

    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'container',
      args
      }
    ]

    return new Container(this._queryTree)
  }

  /**
   * Query a git repository
   */
  git(args: QueryGitArgs): Git {
    this._queryTree = [
      ...this._queryTree,
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
      ...this._queryTree,
      {
      operation: 'host',
      }
    ]

    return new Host(this._queryTree)
  }

}

class Host extends BaseApi {
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

class Git extends BaseApi {
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
class Tree extends BaseApi {
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
class File extends BaseApi {
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

class Container extends BaseApi {

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
  from(args: ContainerFromArgs ): Container {
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
  withEnvVariable(args: ContainerWithEnvVariableArgs): Container {
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
  directory(args: ContainerDirectoryArgs): Container {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'directory',
      args
      }
    ]

    return new Container(this._queryTree)
  }
}

class Directory extends BaseApi {
  
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

class Contents extends BaseApi {
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