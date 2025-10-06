
/**
 * A directory whose contents persist across runs
 */
export class CacheVolume extends BaseClient {
  private readonly _id?: CacheVolumeID = undefined

  /**
   * Constructor is used for internal usage only, do not create object from it.
   */
   constructor(
    ctx?: Context,
     _id?: CacheVolumeID,
   ) {
     super(ctx)

     this._id = _id
   }
  id = async (): Promise<CacheVolumeID> => {
    if (this._id) {
      return this._id
    }

    const ctx = this._ctx.select(
      "id",
    )

    const response: Awaited<CacheVolumeID> = await ctx.execute()

    
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
    ctx?: Context,
   ) {
     super(ctx)

   }

  /**
   * Access a directory on the host
   */
  directory = (path: string, opts?: HostDirectoryOpts): Directory => {

    const ctx = this._ctx.select(
      "directory",
      { path, ...opts },
    )
    return new Directory(ctx)
  }

  /**
   * Lookup the value of an environment variable. Null if the variable is not available.
   */
  envVariable = (name: string): HostVariable => {

    const ctx = this._ctx.select(
      "envVariable",
      { name },
    )
    return new HostVariable(ctx)
  }

  /**
   * The current working directory on the host
   */
  workdir = (opts?: HostWorkdirOpts): Directory => {

    const ctx = this._ctx.select(
      "workdir",
      { ...opts },
    )
    return new Directory(ctx)
  }
}
