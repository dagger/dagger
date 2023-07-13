
export class Container extends BaseClient {
  exec(opts?: ContainerExecOpts): Container {
    return new Container({
      queryTree: [
        ...this._queryTree,
        {
          operation: "exec",
          args: { ...opts },
        },
      ],
      host: this.clientHost,
      sessionToken: this.sessionToken,
    })
  }

  /**
   * Use a function to add to the current object.
   *
   * This allows reusing functionality without breaking the pipeline chain.
   */
  with(arg: (param: Container) => Container) {
    return arg(this)
  }
}
