import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"

interface TooManyNestedObjectsErrorOptions extends DaggerSDKErrorOptions {
  response: unknown
}

/**
 *  Dagger only expects one response value from the engine. If the engine returns more than one value this error is thrown.
 *  @see [TooManyNestedObjectsError - Dagger.io](https://docs.dagger.io/current/sdk/nodejs/reference/classes/common_errors.TooManyNestedObjectsError)
 */
export class TooManyNestedObjectsError extends DaggerSDKError {
  name = "TooManyNestedObjectsError"
  code = "D102"

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
