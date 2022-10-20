import { Engine, GraphQLClient, gql } from "../dist/index.js";
import { Scalars, ContainerExecArgs, InputMaybe } from "./types.js";
import { queryBuilder, queryFlatten } from "./utils.js"

type QueryTree = {
  operation: string
  args?: ContainerExecArgs | InputMaybe<Array<Scalars['String']>>
}


export default class Api {
  queryTree:  QueryTree[]
  
  constructor() {
    this.queryTree = [{
      operation: ""
    }]
  }

  host(): Api {
    this.queryTree.push({
      operation: 'host'
    })

    return this
  }

  workdir(): Api {
    this.queryTree.push({
      operation: 'workdir'
    })

    return this
  }
  
  read(): Api {
    this.queryTree.push({
      operation: 'read'
    })

    return this
  }


  async Id(): Promise<Scalars['DirectoryID'] | []> {
    this.queryTree.push({
      operation: 'id'
    })

    const response = await this.compute()

    return response
  }

  container(): Api {
    this.queryTree.push({
      operation: 'container'
    })

    return this
  }

  from(address: Scalars['ContainerAddress']): Api {
    this.queryTree.push({
      operation: 'from',
      args: address
    })

    return this
  }

  exec(args: ContainerExecArgs): Api {
    this.queryTree.push({
      operation: 'exec',
      args
    })

    return this
  }

  stdout(): Api {
    this.queryTree.push({
      operation: 'stdout',
    })

    return this
  }

  async id(): Promise<Scalars['FileID'] | []> {
    this.queryTree.push({
      operation: 'id',
    })

    const response = await this.compute()

    return response
  }

  async compute() : Promise<string | []> {
    // run the query and return the result.
    const query = queryBuilder(this.queryTree)

    const computeQuery: Promise<{[key:string]: string | []}> = new Promise(resolve  => 
      new Engine({}).run(async (client: GraphQLClient) => {
        const response = await client.request(gql`${query}`)
        resolve(queryFlatten(response));
      })
    )
    const result = await computeQuery;

    return result[Object.keys(result)[0]]
  }
}