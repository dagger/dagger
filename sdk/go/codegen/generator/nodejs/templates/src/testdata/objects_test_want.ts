
/**
 * A directory whose contents persist across runs
 */
export class CacheVolume extends BaseClient {
  private readonly _id?: CacheID = undefined

  /**
   * Constructor is used for internal usage only, do not create object from it.
   */
   constructor(
    parent?: { queryTree?: QueryTree[], host?: string, sessionToken?: string },
     _id?: CacheID,
   ) {
     super(parent)

     this._id = _id
   }
  async id(): Promise<CacheID> {
    if (this._id) {
      return this._id
    }

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
   * Constructor is used for internal usage only, do not create object from it.
   */
   constructor(
    parent?: { queryTree?: QueryTree[], host?: string, sessionToken?: string },
   ) {
     super(parent)

   }

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
