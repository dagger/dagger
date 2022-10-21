import { Engine, GraphQLClient, gql } from "../dist/index.js";
import { ContainerExecArgs } from "./types.js";
import { queryBuilder, queryFlatten } from "./utils.js"

type QueryTree = {
  operation: string
  args?: Record<string, any>
}

export class BaseApi {
  protected _queryTree:  QueryTree[]
  
  constructor(queryTree: QueryTree[]= [{
      operation: ""
    }]) {
    this._queryTree = queryTree
  }

  // This getter is used by mocha tests.
  get queryTree() {
    return this._queryTree;
  }

  protected async _compute() : Promise<Record<string, string>> {
    // run the query and return the result.
    const query = queryBuilder(this._queryTree)

    const computeQuery: Promise<Record<string, string>> = new Promise(resolve  => 
      new Engine({}).run(async (client: GraphQLClient) => {
        const response = await client.request(gql`${query}`)
        resolve(queryFlatten(response));
      })
    )

    const result = await computeQuery;

    return result
  }
}

export default class Api extends BaseApi {
  
  container(): Container {

    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'container'
      }
    ]

    return new Container(this._queryTree)
  }
}

class Container extends BaseApi {

  get getQueryTree() {
    return this._queryTree;
  }
  
  exec(args: ContainerExecArgs): Directory {
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'exec',
      args
      }
    ]
    
    return new Directory(this._queryTree)
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
}

class Directory extends BaseApi {
  
  stdout(): File{
    this._queryTree = [
      ...this._queryTree,
      {
      operation: 'stdout',
      }
    ]

    return new File(this._queryTree)
  }
}

class File extends BaseApi {
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