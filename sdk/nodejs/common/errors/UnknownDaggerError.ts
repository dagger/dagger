import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"
import { errorCodes } from "./errors-codes.js"

/**
 *  This error is thrown if the dagger SDK does not identify the error and just wraps the cause.
 */
export class UnknownDaggerError extends DaggerSDKError {
  name = "UnknownDaggerError"
  code = errorCodes.UnknownDaggerError

  /**
   * @hidden
   */
  constructor(message: string, options: DaggerSDKErrorOptions) {
    super(message, options)
  }
}
