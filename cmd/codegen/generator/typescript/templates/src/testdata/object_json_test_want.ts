
export class JSONValue extends BaseClient {
  private readonly _bytes?: JSON = undefined

  /**
   * Constructor is used for internal usage only, do not create object from it.
   */
   constructor(
    ctx?: Context,
     _bytes?: JSON,
   ) {
     super(ctx)

     this._bytes = _bytes
   }
  bytes = async (
    opts?: JSONValueBytesOpts): Promise<JSON> => {
    if (this._bytes) {
      return this._bytes
    }

    const ctx = this._ctx.select(
      "bytes",
      { ...opts},
    )

    const response: Awaited<JSON> = await ctx.execute()

    
    return response
  }
}
