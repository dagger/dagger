import { ClientError } from "graphql-request"

import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"
import { ERROR_CODES, ERROR_NAMES } from "./errors-codes.js"

interface GraphQLRequestErrorOptions extends DaggerSDKErrorOptions {
  error: ClientError
}

/**
 *  This error originates from the dagger engine. It means that some error was thrown and sent back via GraphQL.
 */
export class GraphQLRequestError extends DaggerSDKError {
  name = ERROR_NAMES.GraphQLRequestError
  code = ERROR_CODES.GraphQLRequestError

  /**
   *  The query and variables, which caused the error.
   */
  requestContext: ClientError["request"]

  /**
   *  the GraphQL response containing the error.
   */
  response: ClientError["response"]

  /**
   *  @hidden
   */
  constructor(message: string, options: GraphQLRequestErrorOptions) {
    super(message, options)
    this.requestContext = options.error.request
    this.response = options.error.response
  }
}
