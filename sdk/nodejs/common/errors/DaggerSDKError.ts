import { log } from "../utils.js"

export interface DaggerSDKErrorOptions {
  cause?: Error
}

/**
 * The base error. Every other error inherits this error.
 * @see [DaggerSDKError - Dagger.io](https://docs.dagger.io/current/sdk/nodejs/reference/classes/common_errors.DaggerSDKError)
 */
export abstract class DaggerSDKError extends Error {
  /**
   * The name of the dagger error.
   */
  abstract name: string

  /**
   * The dagger specific error code.
   * Use this to identify dagger errors programmatically.
   */
  abstract code: string

  /**
   * The original error, which caused the DaggerSDKError.
   */
  cause?: Error

  protected constructor(message: string, options?: DaggerSDKErrorOptions) {
    super(message)
    this.cause = options?.cause
  }

  /**
   * @hidden
   */
  get [Symbol.toStringTag]() {
    return this.name
  }

  /**
   * Pretty prints the error
   */
  printStackTrace() {
    log(this.stack)
  }
}
