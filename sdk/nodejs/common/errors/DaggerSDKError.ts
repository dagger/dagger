import { log } from "../utils.js"
import { ErrorCodes, ErrorNames } from "./errors-codes"

export interface DaggerSDKErrorOptions {
  cause?: Error
}

/**
 * The base error. Every other error inherits this error.
 */
export abstract class DaggerSDKError extends Error {
  /**
   * The name of the dagger error.
   */
  abstract readonly name: ErrorNames

  /**
   * The dagger specific error code.
   * Use this to identify dagger errors programmatically.
   */
  abstract readonly code: ErrorCodes

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
