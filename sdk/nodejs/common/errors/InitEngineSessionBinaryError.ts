import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"

/**
 *  This error is thrown if the dagger binary cannot be copied from the dagger docker image and copied to the local host.
 */
export class InitEngineSessionBinaryError extends DaggerSDKError {
  name = "InitEngineSessionBinaryError"
  code = "D106"

  /**
   *  @hidden
   */
  constructor(message: string, options?: DaggerSDKErrorOptions) {
    super(message, options)
  }
}
