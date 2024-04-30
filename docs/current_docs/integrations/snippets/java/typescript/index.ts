import {
  dag,
  File,
  Directory,
  Secret,
  object,
  func,
} from "@dagger.io/dagger"

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class MyModule {
  @func()
  build(source: Directory): File {
    return dag
      .java()
      .withJdk("17")
      .withMaven("3.9.5")
      .withProject(source.withoutDirectory("dagger"))
      .maven(["package"])
      .file("target/spring-petclinic-3.2.0-SNAPSHOT.jar")
  }

  @func()
  async publish(
    source: Directory,
    version: string,
    registryAddress: string,
    registryUsername: string,
    registryPassword: Secret,
    imageName: string,
  ): Promise<string> {
    return await dag
      .container()
      .from("eclipse-temurin:17-alpine")
      .withLabel("org.opencontainers.image.title", "Java with Dagger")
      .withLabel("org.opencontainers.image.version", version)
      .withFile("/app/spring-petclinic-3.2.0-SNAPSHOT.jar", this.build(source))
      .withEntrypoint([
        "java",
        "-jar",
        "/app/spring-petclinic-3.2.0-SNAPSHOT.jar",
      ])
      .withRegistryAuth(registryAddress, registryUsername, registryPassword)
      .publish(`${registryAddress}/${registryUsername}/${imageName}`)
  }
}
