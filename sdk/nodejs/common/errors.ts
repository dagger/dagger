import {
  GraphQLRequestContext,
  GraphQLResponse,
} from "graphql-request/dist/types"

/*
###################
DaggerSDKError
###################
*/

/**
 *  TODO: ADD Description
 */
export interface DaggerSDKErrorOptions {
  cause?: Error
}
/**
 *  TODO: ADD Description
 */
export abstract class DaggerSDKError extends Error {
  abstract name: string
  abstract code: string
  cause?: Error

  constructor(message: string, options?: DaggerSDKErrorOptions) {
    super(message)
    this.cause = options?.cause
  }
}

/*
###################
GraphQLRequestError
###################
*/

/**
 *  TODO: ADD Description
 */
export interface GraphQLRequestErrorOptions extends DaggerSDKErrorOptions {
  response: GraphQLResponse
  request: GraphQLRequestContext
}

/*
  TODO: ADD Description
*/
export class GraphQLRequestError extends DaggerSDKError {
  name = "GraphQLRequestError"
  code = "100"

  requestContext: GraphQLRequestContext
  response: GraphQLResponse

  constructor(message: string, options: GraphQLRequestErrorOptions) {
    super(message, options)
    this.requestContext = options.request
    this.response = options.response
  }
}

/*
###################
UnknownDaggerError
###################
*/

/**
 *  TODO: ADD Description
 */
export class UnknownDaggerError extends DaggerSDKError {
  name = "UnknownDaggerError"
  code = "101"

  constructor(message: string, options: DaggerSDKErrorOptions) {
    super(message, options)
  }
}

/*
###################
TooManyNestedObjectsError
###################
*/

/**
 *  TODO: ADD Description
 */
export interface TooManyNestedObjectsErrorOptions
  extends DaggerSDKErrorOptions {
  response: unknown
}

/**
 *  TODO: ADD Description
 */
export class TooManyNestedObjectsError extends DaggerSDKError {
  name = "TooManyNestedObjectsError"
  code = "102"

  response: unknown

  constructor(message: string, options: TooManyNestedObjectsErrorOptions) {
    super(message, options)
    this.response = options.response
  }
}

/*
###################
EngineSessionPortParseError
###################
*/

/**
 *  TODO: ADD Description
 */
export interface EngineSessionPortParseErrorOptions
  extends DaggerSDKErrorOptions {
  parsedLine: string
}

/**
 *  TODO: ADD Description
 */
export class EngineSessionPortParseError extends DaggerSDKError {
  name = "EngineSessionPortError"
  code = "103"

  parsedLine?: string

  constructor(message: string, options?: EngineSessionPortParseErrorOptions) {
    super(message, options)
    if (options) this.parsedLine = options.parsedLine
  }
}

/*
###################
DockerImageRefValidationError
###################
*/

/**
 *  TODO: ADD Description
 */
export interface DockerImageRefValidationErrorOptions
  extends DaggerSDKErrorOptions {
  ref: string
}

/**
 *  TODO: ADD Description
 */
export class DockerImageRefValidationError extends DaggerSDKError {
  name = "DockerImageRefValidationError"
  code = "104"

  ref: string

  constructor(message: string, options: DockerImageRefValidationErrorOptions) {
    super(message, options)
    this.ref = options?.ref
  }
}

/*
###################
InitEngineSessionBinaryError
###################
*/

/**
 *  TODO: ADD Description
 */
export class InitEngineSessionBinaryError extends DaggerSDKError {
  name = "InitEngineSessionBinaryError"
  code = "105"

  constructor(message: string, options?: DaggerSDKErrorOptions) {
    super(message, options)
  }
}
