
export class Container extends BaseClient {

  /**
   * Constructor is used for internal usage only, do not create object from it.
   */
   constructor(
    ctx?: Context,
   ) {
     super(ctx)

   }
  exec = (opts?: ContainerExecOpts): Container => {

    const ctx = this._ctx.select(
      "exec",
      { ...opts },
    )
    return new Container(ctx)
  }

  /**
   * Call the provided function with current Container.
   *
   * This is useful for reusability and readability by not breaking the calling chain.
   */
  with = (arg: (param: Container) => Container) => {
    return arg(this)
  }
}
