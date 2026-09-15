import { dag, Container, object, func } from "@dagger.io/dagger"

@object()
class HelloDagger {
  /**
   * Returns a base container
   */
  @func()
  base(): Container {
    return dag.container().from("cgr.dev/chainguard/wolfi-base")
  }

  /**
   * Builds on top of base container and returns a new container
   */
  @func()
  build(): Container {
    return this.base().withExec(["apk", "add", "bash", "git"])
  }

  /**
   * Builds and publishes a container
   */
  @func()
  async buildAndPublish(): Promise<string> {
    return await this.build().publish("ttl.sh/bar")
  }
}
