import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"
import { ERROR_CODES, ERROR_NAMES } from "./errors-codes.js"

interface TooManyNestedObjectsErrorOptions extends DaggerSDKErrorOptions {
  response: unknown
}

/**
 *  Dagger only expects one response value from the engine. If the engine returns more than one value this error is thrown.
 */
export class TooManyNestedObjectsError extends DaggerSDKError {
  name = ERROR_NAMES.TooManyNestedObjectsError
  code = ERROR_CODES.TooManyNestedObjectsError

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
