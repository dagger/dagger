
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
   * Call the provided function with current Container.
   *
   * This is useful for reusability and readability by not breaking the calling chain.
   */
  with(arg: (param: Container) => Container) {
    return arg(this)
  }
}
