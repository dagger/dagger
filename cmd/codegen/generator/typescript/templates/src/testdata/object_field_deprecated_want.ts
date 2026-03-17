
export class Test extends BaseClient {
  private readonly _legacyField?: string = undefined

  /**
   * Constructor is used for internal usage only, do not create object from it.
   */
   constructor(
    ctx?: Context,
     _legacyField?: string,
   ) {
     super(ctx)

     this._legacyField = _legacyField
   }

  /**
   * @deprecated This field is deprecated and will be removed in future versions.
   */
  legacyField = async (): Promise<string> => {
    if (this._legacyField) {
      return this._legacyField
    }

    const ctx = this._ctx.select(
      "legacyField",
    )

    const response: Awaited<string> = await ctx.execute()

    
    return response
  }
}
