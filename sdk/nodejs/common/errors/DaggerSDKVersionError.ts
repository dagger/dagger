export interface GetSDKVersionErrorOptions {
  cause?: Error
}

export class GetSDKVersionError extends Error {
  readonly name = "GetSDKVersionError"
  readonly code = "GET_SDK_VERSION_ERROR"
  cause?: Error

  constructor(message: string, options?: GetSDKVersionErrorOptions) {
    super(message)
    this.cause = options?.cause
  }

  get [Symbol.toStringTag]() {
    return this.name
  }
}
