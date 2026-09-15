
export type ContainerExecOpts = {
  /**
   * Command to run instead of the container's default command
   */
  args?: string[]

  /**
   * Content to write to the command's standard input before closing
   */
  stdin?: string

  /**
   * Redirect the command's standard output to a file in the container
   */
  redirectStdout?: string

  /**
   * Redirect the command's standard error to a file in the container
   */
  redirectStderr?: string

  /**
   * Provide dagger access to the executed command
   * Do not use this option unless you trust the command being executed
   * The command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM
   */
  experimentalPrivilegedNesting?: boolean
}
