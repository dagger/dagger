import { Writable } from "node:stream"

/**
 * ConnectOpts defines option used to connect to an engine.
 */
export interface ConnectOpts {
  /**
   * Use to overwrite Dagger workdir
   * @defaultValue process.cwd()
   */
  Workdir?: string

  /**
     * Enable logs output
     * @example
     * LogOutput
     * ```ts
     * connect(async (client: Client) => {
    const source = await client.host().workdir().id()
    ...
    }, {LogOutput: process.stdout})
     ```
     */
  LogOutput?: Writable
}
