import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"

/**
 *  This error is thrown if the dagger SDK does not identify the error and just wraps the cause.
 */
export class UnknownDaggerError extends DaggerSDKError {
  name = "UnknownDaggerError"
  code = "D101"

  /**
   * @hidden
   */
  constructor(message: string, options: DaggerSDKErrorOptions) {
    super(message, options)
  }
}
