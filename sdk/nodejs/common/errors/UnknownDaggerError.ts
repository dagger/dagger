import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"
import { ERROR_CODES } from "./errors-codes.js"

/**
 *  This error is thrown if the dagger SDK does not identify the error and just wraps the cause.
 */
export class UnknownDaggerError extends DaggerSDKError {
  readonly name = "UnknownDaggerError"
  readonly code = ERROR_CODES.UnknownDaggerError

  /**
   * @hidden
   */
  constructor(message: string, options: DaggerSDKErrorOptions) {
    super(message, options)
  }
}
