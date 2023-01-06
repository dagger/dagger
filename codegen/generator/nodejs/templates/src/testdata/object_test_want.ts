
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
}
