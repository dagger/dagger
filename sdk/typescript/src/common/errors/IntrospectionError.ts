import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"
import { ERROR_CODES, ERROR_NAMES } from "./errors-codes.js"

export class IntrospectionError extends DaggerSDKError {
  name = ERROR_NAMES.IntrospectionError
  code = ERROR_CODES.IntrospectionError

  constructor(message: string, options?: DaggerSDKErrorOptions) {
    super(message, options)
  }
}
