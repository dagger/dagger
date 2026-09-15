
export class TestFooer extends BaseClient {
  private readonly _foo?: string = undefined

  /**
   * Constructor is used for internal usage only, do not create object from it.
   */
   constructor(
    ctx?: Context,
     _foo?: string,
   ) {
     super(ctx)

     this._foo = _foo
   }

  /**
   * @deprecated Use Bar instead.
   */
  foo = async (
    opts?: TestFooerFooOpts): Promise<string> => {
    if (this._foo) {
      return this._foo
    }

    const ctx = this._ctx.select(
      "foo",
      { ...opts},
    )

    const response: Awaited<string> = await ctx.execute()

    
    return response
  }
}
