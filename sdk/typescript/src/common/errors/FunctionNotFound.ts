import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"
import { ERROR_CODES, ERROR_NAMES } from "./errors-codes.js"

export class FunctionNotFound extends DaggerSDKError {
  name = ERROR_NAMES.ExecError
  code = ERROR_CODES.ExecError

  constructor(message: string, options?: DaggerSDKErrorOptions) {
    super(message, options)
  }
}
