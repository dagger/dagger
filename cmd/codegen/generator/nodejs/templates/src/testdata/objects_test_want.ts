
/**
 * A directory whose contents persist across runs
 */
export class CacheVolume extends BaseClient {
  private readonly _id?: CacheVolumeID = undefined

  /**
   * Constructor is used for internal usage only, do not create object from it.
   */
   constructor(
    parent?: { queryTree?: QueryTree[], ctx: Context },
     _id?: CacheVolumeID,
   ) {
     super(parent)

     this._id = _id
   }
  id = async (): Promise<CacheVolumeID> => {
    if (this._id) {
      return this._id
    }

    const response: Awaited<CacheVolumeID> = await computeQuery(
      [
        ...this._queryTree,
        {
          operation: "id",
        },
      ],
      await this._ctx.connection()
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
    parent?: { queryTree?: QueryTree[], ctx: Context },
   ) {
     super(parent)

   }

  /**
   * Access a directory on the host
   */
  directory = (path: string, opts?: HostDirectoryOpts): Directory => {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "directory",
          args: { path, ...opts },
        },
      ],
      ctx: this._ctx,
    })
  }

  /**
   * Lookup the value of an environment variable. Null if the variable is not available.
   */
  envVariable = (name: string): HostVariable => {
    return new HostVariable({
      queryTree: [
        ...this._queryTree,
        {
          operation: "envVariable",
          args: { name },
        },
      ],
      ctx: this._ctx,
    })
  }

  /**
   * The current working directory on the host
   */
  workdir = (opts?: HostWorkdirOpts): Directory => {
    return new Directory({
      queryTree: [
        ...this._queryTree,
        {
          operation: "workdir",
          args: { ...opts },
        },
      ],
      ctx: this._ctx,
    })
  }
}
