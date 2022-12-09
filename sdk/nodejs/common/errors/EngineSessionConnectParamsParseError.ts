import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"

interface EngineSessionConnectParamsParseErrorOptions
  extends DaggerSDKErrorOptions {
  parsedLine: string
}

/**
 * This error is thrown if the EngineSession does not manage to parse the required connection parameters from the session binary
 * This can happen if
 * - Reading the parameters times out after 30 seconds
 * - The parsed line does not match the expected json format
 * - the reader does not read a single line
 */
export class EngineSessionConnectParamsParseError extends DaggerSDKError {
  name = "EngineSessionConnectParamsParseError"
  code = "D103"

  /**
   *  the line, which caused the error during parsing, if the error was caused because of parsing.
   */
  parsedLine?: string

  /**
   * @hidden
   */
  constructor(
    message: string,
    options?: EngineSessionConnectParamsParseErrorOptions
  ) {
    super(message, options)
    if (options) this.parsedLine = options.parsedLine
  }
}
