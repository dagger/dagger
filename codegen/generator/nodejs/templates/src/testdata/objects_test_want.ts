
/**
 * A directory whose contents persist across runs
 */
export class CacheVolume extends BaseClient {

  async id(): Promise<CacheID> {
    const response: Awaited<CacheID> = await computeQuery(
      [
        ...this._queryTree,
        {
          operation: "id",
        },
      ],
      this.client
    )

    return response
  }
}

/**
 * Information about the host execution environment
 */
export class Host extends BaseClient {


  /**
   * Access a directory on the host
   */
  directory(path: string, opts?: HostDirectoryOpts): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "directory",
          args: { path, ...opts },
        },
      ],
      host: this.clientHost,
      sessionToken: this.sessionToken,
    })
  }

  /**
   * Lookup the value of an environment variable. Null if the variable is not available.
   */
  envVariable(name: string): HostVariable {
    return new HostVariable({
      queryTree: [
        ...this._queryTree,
        {
          operation: "envVariable",
          args: { name },
        },
      ],
      host: this.clientHost,
      sessionToken: this.sessionToken,
    })
  }

  /**
   * The current working directory on the host
   */
  workdir(opts?: HostWorkdirOpts): Directory {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "workdir",
          args: { ...opts },
        },
      ],
      host: this.clientHost,
      sessionToken: this.sessionToken,
    })
  }
}
