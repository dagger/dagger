import {
  dag,
  Container,
  Directory,
  Secret,
  object,
  func,
} from "@dagger.io/dagger"

@object()
class MyModule {
  /*
   * Return container image with application source code and dependencies
   */
  @func()
  build(source: Directory): Container {
    return dag
      .container()
      .from("php:8.2")
      .withExec(["apt-get", "update"])
      .withExec(["apt-get", "install", "--yes", "git-core", "zip", "curl"])
      .withDirectory("/var/www", source.withoutDirectory("dagger"))
      .withWorkdir("/var/www")
      .withExec(["chmod", "-R", "775", "/var/www"])
      .withExec([
        "sh",
        "-c",
        "curl -sS https://getcomposer.org/installer | php -- --install-dir=/usr/local/bin --filename=composer",
      ])
      .withEnvVariable("PATH", "./vendor/bin:$PATH", { expand: true })
      .withExec(["composer", "install"])
  }

  /*
   * Return result of unit tests
   */
  @func()
  async test(source: Directory): Promise<string> {
    return await this.build(source).withExec(["phpunit"]).stdout()
  }

  /*
   * Return address of published container image
   */
  @func()
  async publish(
    source: Directory,
    version: string,
    registryAddress: string,
    registryUsername: string,
    registryPassword: Secret,
    imageName: string,
  ): Promise<string> {
    const image = this.build(source)
      .withLabel("org.opencontainers.image.title", "Laravel with Dagger")
      .withLabel("org.opencontainers.image.version", version)
      .withEntrypoint(["php", "-S", "0.0.0.0:8080", "-t", "public"])
      .withExposedPort(8080)

    const address = await image
      .withRegistryAuth(registryAddress, registryUsername, registryPassword)
      .publish(`${registryAddress}/${registryUsername}/${imageName}`)

    return address
  }
}
