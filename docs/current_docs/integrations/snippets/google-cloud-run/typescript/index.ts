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
  /**
   * Build an image
   */
  @func()
  build(source: Directory): Container {
    return dag
      .container()
      .from("node:21")
      .withDirectory("/home/node", source)
      .withWorkdir("/home/node")
      .withExec(["npm", "install"])
      .withEntrypoint(["npm", "start"])
  }

  /**
   * Publish an image
   *
   * example: dagger call publish --source . --project PROJECT
   *   --location LOCATION --repository REPOSITORY/APPNAME
   *   --credential env:GOOGLE_JSON
   */
  @func()
  async publish(
    source: Directory,
    project: string,
    location: string,
    repository: string,
    credential: Secret,
  ): Promise<string> {
    const registry = `${location}-docker.pkg.dev/${project}/${repository}`
    return await this.build(source)
      .withRegistryAuth(`${location}-docker.pkg.dev`, "_json_key", credential)
      .publish(registry)
  }

  /**
   * Deploy an image to Google Cloud Run
   *
   * example: dagger call deploy --source . --project PROJECT
   *  --registry-location LOCATION --repository REPOSITORY/APPNAME
   *  --service-location LOCATION --service SERVICE
   *  --credential env:GOOGLE_JSON
   */
  @func()
  async deploy(
    source: Directory,
    project: string,
    registryLocation: string,
    repository: string,
    serviceLocation: string,
    service: string,
    credential: Secret,
  ): Promise<string> {
    const addr = await this.publish(
      source,
      project,
      registryLocation,
      repository,
      credential,
    )

    return dag
      .googleCloudRun()
      .updateService(project, serviceLocation, service, addr, 3000, credential)
  }
}
