import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"
import { ERROR_CODES } from "./errors-codes.js"

type EngineSessionEOFErrorOptions = DaggerSDKErrorOptions

/**
 * This error is thrown if the EngineSession does not manage to parse the required port successfully because a EOF is read before any valid port.
 * This usually happens if no connection can be established.
 */
export class EngineSessionEOFError extends DaggerSDKError {
  name = "EngineSessionEOFError"
  code = ERROR_CODES.EngineSessionEOFError

  /**
   * @hidden
   */
  constructor(message: string, options?: EngineSessionEOFErrorOptions) {
    super(message, options)
  }
}
