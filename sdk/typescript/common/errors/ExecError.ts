import { DaggerSDKError, DaggerSDKErrorOptions } from "./DaggerSDKError.js"
import { ERROR_CODES, ERROR_NAMES } from "./errors-codes.js"

interface ExecErrorOptions extends DaggerSDKErrorOptions {
  cmd: string[]
  exitCode: number
  stdout: string
  stderr: string
}

/**
 *  API error from an exec operation in a pipeline.
 */
export class ExecError extends DaggerSDKError {
  name = ERROR_NAMES.ExecError
  code = ERROR_CODES.ExecError

  /**
   *  The command that caused the error.
   */
  cmd: string[]

  /**
   *  The exit code of the command.
   */
  exitCode: number

  /**
   * The stdout of the command.
   */
  stdout: string

  /**
   * The stderr of the command.
   */
  stderr: string

  /**
   *  @hidden
   */
  constructor(message: string, options: ExecErrorOptions) {
    super(message, options)
    this.cmd = options.cmd
    this.exitCode = options.exitCode
    this.stdout = options.stdout
    this.stderr = options.stderr
  }

  toString(): string {
    return `${super.toString()}\nStdout:\n${this.stdout}\nStderr:\n${this.stderr}`
  }
}
