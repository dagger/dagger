import { dag, Secret, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Tag a container image multiple times and publish it to a private registry
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
  ): Promise<string[]> {
    const tags = ["latest", "1.0-alpine", "1.0", "1.0.0"]

    const addr: string[] = []

    const container = dag
      .container()
      .from("nginx:1.23-alpine")
      .withNewFile("/usr/share/nginx/html/index.html", "Hello from Dagger!", {
        permissions: 0o400,
      })
      .withRegistryAuth(registry, username, password)

    for (const tag in tags) {
      const a = await container.publish(
        `${registry}/${username}/my-nginx:${tags[tag]}`,
      )
      addr.push(a)
    }

    return addr
  }
}
