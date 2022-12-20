import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"
import { ERROR_CODES } from "./errors-codes.js"

interface DockerImageRefValidationErrorOptions extends DaggerSDKErrorOptions {
  ref: string
}

/**
 *  This error is thrown if the passed image reference does not pass validation and is not compliant with the
 *  DockerImage constructor.
 */
export class DockerImageRefValidationError extends DaggerSDKError {
  name = "DockerImageRefValidationError"
  code = ERROR_CODES.DockerImageRefValidationError

  /**
   *  The docker image reference, which caused the error.
   */
  ref: string

  /**
   *  @hidden
   */
  constructor(message: string, options: DockerImageRefValidationErrorOptions) {
    super(message, options)
    this.ref = options?.ref
  }
}
