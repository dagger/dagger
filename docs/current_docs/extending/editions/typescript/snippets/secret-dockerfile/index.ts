import { dag, object, func, Secret } from "@dagger.io/dagger"

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
    // Ensure the Dagger secret's name matches what the Dockerfile
    // expects as the id for the secret mount.
    const buildSecret = dag.setSecret("gh-secret", await secret.plaintext())

    return source.dockerBuild({ secrets: [buildSecret] })
  }
}
