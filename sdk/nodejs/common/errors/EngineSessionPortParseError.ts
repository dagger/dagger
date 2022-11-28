import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"

interface EngineSessionPortParseErrorOptions extends DaggerSDKErrorOptions {
  parsedLine: string
}

/**
 * This error is thrown if the EngineSession does not manage to parse the required port successfully.
 * This can happen if
 * - Reading the port times out after 30 seconds
 * - The parsed port is not a number
 * - the reader does not read a single line
 * @see [EngineSessionPortParseError - Dagger.io](current/sdk/nodejs/reference/classes/common_errors.EngineSessionPortParseError)
 */
export class EngineSessionPortParseError extends DaggerSDKError {
  name = "EngineSessionPortError"
  code = "D103"

  /**
   *  the line, which caused the error during parsing, if the error was caused because of parsing.
   */
  parsedLine?: string

  /**
   * @hidden
   */
  constructor(message: string, options?: EngineSessionPortParseErrorOptions) {
    super(message, options)
    if (options) this.parsedLine = options.parsedLine
  }
}
