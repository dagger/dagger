import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.ts"
import { ERROR_CODES, ERROR_NAMES } from "./errors-codes.ts"

/**
 *  This error is thrown if the dagger binary cannot be copied from the dagger docker image and copied to the local host.
 */
export class InitEngineSessionBinaryError extends DaggerSDKError {
  name = ERROR_NAMES.InitEngineSessionBinaryError
  code = ERROR_CODES.InitEngineSessionBinaryError

  /**
   *  @hidden
   */
  constructor(message: string, options?: DaggerSDKErrorOptions) {
    super(message, options)
  }
}
