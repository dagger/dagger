import { object, func, Secret } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Build a Container from a Dockerfile
   */
  @func()
  async build(
    /**
     * The source code to build
     */
    source: Directory,
    /**
     * The secret to use in the Dockerfile
     */
    secret: Secret,
  ): Promise<Container> {
    const secretName = await secret.name()
    return source.dockerBuild({
      dockerfile: "Dockerfile",
      buildArgs: [{ name: "gh-secret", value: secretName }],
      secrets: [secret],
    })
  }
}
