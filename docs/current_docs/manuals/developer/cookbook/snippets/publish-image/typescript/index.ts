import { dag, Secret, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Publish a container image to a private registry
   */
  @func()
  async publish(
    /**
     * Registry address
     */
    registry: string,
    /**
     * Registry username
     */
    username: string,
    /**
     * Registry password
     */
    password: Secret,
  ): Promise<string> {
    return await dag
      .container()
      .from("nginx:1.23-alpine")
      .withNewFile("/usr/share/nginx/html/index.html", {
        contents: "Hello from Dagger!",
        permissions: 0o400,
      })
      .withRegistryAuth(registry, username, password)
      .publish(`${registry}/${username}/my-nginx`)
  }
}
