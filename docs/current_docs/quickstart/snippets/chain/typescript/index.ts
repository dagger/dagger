import { dag, Container, object, func } from "@dagger.io/dagger"

@object()
class HelloDagger {
  /**
   * Returns a container
   */
  @func()
  foo(): Container {
    return dag.container().from("cgr.dev/chainguard/wolfi-base")
  }

  /**
   * Publishes a container
   */
  @func()
  async bar(): Promise<string> {
    return await this.foo().publish("ttl.sh/bar")
  }
}
