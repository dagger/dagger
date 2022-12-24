import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"
import { ERROR_CODES } from "./errors-codes.js"

interface TooManyNestedObjectsErrorOptions extends DaggerSDKErrorOptions {
  response: unknown
}

/**
 *  Dagger only expects one response value from the engine. If the engine returns more than one value this error is thrown.
 */
export class TooManyNestedObjectsError extends DaggerSDKError {
  readonly name = "TooManyNestedObjectsError"
  readonly code = ERROR_CODES.TooManyNestedObjectsError

  /**
   *  the response containing more than one value.
   */
  response: unknown

  /**
   * @hidden
   */
  constructor(message: string, options: TooManyNestedObjectsErrorOptions) {
    super(message, options)
    this.response = options.response
  }
}
