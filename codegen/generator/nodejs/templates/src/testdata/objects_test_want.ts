
/**
 * A directory whose contents persist across runs
 */
export class CacheVolume extends BaseClient {
  async id(): Promise<CacheID> {
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
  with(arg: (param: CacheVolume) => CacheVolume) {
    return arg(this)
  }
}

/**
 * Information about the host execution environment
 */
export class Host extends BaseClient {


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
  with(arg: (param: Host) => Host) {
    return arg(this)
  }
}
