import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"

interface EngineSessionConnectionTimeoutErrorOptions
  extends DaggerSDKErrorOptions {
  timeOutDuration: number
}

/**
 * This error is thrown if the EngineSession does not manage to parse the required port successfully because the sessions connection timed out.
 */
export class EngineSessionConnectionTimeoutError extends DaggerSDKError {
  name = "EngineSessionConnectionTimeoutError"
  code = "D104"

  /**
   * The duration until the timeout occurred in ms.
   */
  timeOutDuration: number

  /**
   * @hidden
   */
  constructor(
    message: string,
    options: EngineSessionConnectionTimeoutErrorOptions
  ) {
    super(message, options)
    this.timeOutDuration = options.timeOutDuration
  }
}
