import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"
import { ERROR_CODES, ERROR_NAMES } from "./errors-codes.js"

interface EngineSessionConnectParamsParseErrorOptions
  extends DaggerSDKErrorOptions {
  parsedLine: string
}

/**
 * This error is thrown if the EngineSession does not manage to parse the required connection parameters from the session binary
 */
export class EngineSessionConnectParamsParseError extends DaggerSDKError {
  name = ERROR_NAMES.EngineSessionConnectParamsParseError
  code = ERROR_CODES.EngineSessionConnectParamsParseError

  /**
   *  the line, which caused the error during parsing, if the error was caused because of parsing.
   */
  parsedLine: string

  /**
   * @hidden
   */
  constructor(
    message: string,
    options: EngineSessionConnectParamsParseErrorOptions,
  ) {
    super(message, options)
    this.parsedLine = options.parsedLine
  }
}
