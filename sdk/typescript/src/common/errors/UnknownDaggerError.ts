import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"
import { ERROR_CODES, ERROR_NAMES } from "./errors-codes.js"

/**
 *  This error is thrown if the dagger SDK does not identify the error and just wraps the cause.
 */
export class UnknownDaggerError extends DaggerSDKError {
  name = ERROR_NAMES.UnknownDaggerError
  code = ERROR_CODES.UnknownDaggerError

  /**
   * @hidden
   */
  constructor(message: string, options: DaggerSDKErrorOptions) {
    super(message, options)
  }
}
