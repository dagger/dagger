import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"
import { errorCodes } from "./errors-codes.js"

interface TooManyNestedObjectsErrorOptions extends DaggerSDKErrorOptions {
  response: unknown
}

/**
 *  Dagger only expects one response value from the engine. If the engine returns more than one value this error is thrown.
 */
export class TooManyNestedObjectsError extends DaggerSDKError {
  name = "TooManyNestedObjectsError"
  code = errorCodes.TooManyNestedObjectsError

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
