import {
  GraphQLRequestContext,
  GraphQLResponse,
} from "graphql-request/dist/types"
import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"

interface GraphQLRequestErrorOptions extends DaggerSDKErrorOptions {
  response: GraphQLResponse
  request: GraphQLRequestContext
}

/**
 *  This error originates from the dagger engine. It means that some error was thrown and sent back via GraphQL.
 *  @see [GraphQLRequestError - Dagger.io](current/sdk/nodejs/reference/classes/common_errors.GraphQLRequestError)
 */
export class GraphQLRequestError extends DaggerSDKError {
  public name = "GraphQLRequestError"
  public code = "D100"

  /**
   *  The query and variables, which caused the error.
   */
  requestContext: GraphQLRequestContext

  /**
   *  the GraphQL response containing the error.
   */
  response: GraphQLResponse

  /**
   *  @hidden
   */
  constructor(message: string, options: GraphQLRequestErrorOptions) {
    super(message, options)
    this.requestContext = options.request
    this.response = options.response
  }
}
