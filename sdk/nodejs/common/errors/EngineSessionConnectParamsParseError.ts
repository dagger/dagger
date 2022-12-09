import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"

interface EngineSessionConnectParamsParseErrorOptions
  extends DaggerSDKErrorOptions {
  parsedLine: string
}

/**
 * This error is thrown if the EngineSession does not manage to parse the required connection parameters from the session binary
 */
export class EngineSessionConnectParamsParseError extends DaggerSDKError {
  name = "EngineSessionConnectParamsParseError"
  code = "102"

  /**
   *  the line, which caused the error during parsing, if the error was caused because of parsing.
   */
  parsedLine: string

  /**
   * @hidden
   */
  constructor(
    message: string,
    options: EngineSessionConnectParamsParseErrorOptions
  ) {
    super(message, options)
    this.parsedLine = options.parsedLine
  }
}
