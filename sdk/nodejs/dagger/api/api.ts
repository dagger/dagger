import { Engine, GraphQLClient, gql } from "../../dist/index.js";
import { ContainerExecArgs } from "./types.js";
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

  protected async _compute() : Promise<Record<string, string>> {
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
  
  container(args?: {id: any}): Container {

    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'container',
      args
      }
    ]

    return new Container(this._queryTree)
  }

  git(args: {url: string}): Git {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'git',
      args
      }
    ]

    return new Git(this._queryTree)
  }

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
  workdir(args?: {exclude?: Array<string>, include?: Array<string>}): Directory {
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
  branch(args: {name: string}): Tree {
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
  file(args: {path: string}): Contents {
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

  from(args: { address: String } ): Container {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'from',
      args
      }
    ]
    
    return new Container(this._queryTree)
  }

  fs(): Directory {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'fs',
      }
    ]
    
    return new Directory(this._queryTree)
  }

  withFS(args: {id: string}): Container {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'withFS',
      args
      }
    ]
    
    return new Container(this._queryTree)
  }

  withMountedDirectory(args: {path: string, source: string}): Container {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'withMountedDirectory',
      args
      }
    ]
    
    return new Container(this._queryTree)
  }

  async id(): Promise<Record<string, string>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'id',
      }
    ]

    const response: Record<string, string> = await this._compute()

    return response
  }

  stdout(): Contents {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'stdout',
      }
    ]
    
    return new Contents(this._queryTree)
  }

  withWorkdir(args: {path: string}): Container {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'withWorkdir',
      args
      }
    ]

    return new Container(this._queryTree)
  }

  withEnvVariable(args: {name: string,value: string}): Container {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'withEnvVariable',
      args
      }
    ]

    return new Container(this._queryTree)
  }

  directory(args: {path: string}): Container {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'directory',
      args
      }
    ]

    return new Container(this._queryTree)
  }

  async entries(args?: {path: string}): Promise<Record<string, string>>  {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'entries',
      args
      }
    ]

    const response: Record<string, string> = await this._compute()

    return response
  }
}

class Directory extends BaseApi {
  
  async id(): Promise<Record<string, string>> {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'id'
      }
    ]

    const response: Record<string, string> = await this._compute()

    return response
  }

  stdout(): Contents {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'stdout',
      }
    ]

    return new Contents(this._queryTree)
  }
}

class Contents extends BaseApi {
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
