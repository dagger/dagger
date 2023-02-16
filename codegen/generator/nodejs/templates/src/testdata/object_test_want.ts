

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
   * Chain objects together
   * @example
   * ```ts
   *	function AddAFewMounts(c) {
   *			return c
   *			.withMountedDirectory("/foo", new Client().host().directory("/Users/slumbering/forks/dagger"))
   *			.withMountedDirectory("/bar", new Client().host().directory("/Users/slumbering/forks/dagger/sdk/nodejs"))
   *	}
   *
   * connect(async (client) => {
   *		const tree = await client
   *			.container()
   *			.from("alpine")
   *			.withWorkdir("/foo")
   *			.with(AddAFewMounts)
   *			.withExec(["ls", "-lh"])
   *			.stdout()
   * })
   *```
   */
  with(arg: (param: Container) => Container) {
    return arg(this)
  }
}
