import { dag, Container, Directory, Secret, object, func } from "@dagger.io/dagger"

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class MyModule {

  source: Directory

  // constructor
  constructor (source: Directory) {
    this.source = source
  }

  @func()
  build(): Container {
    return dag.container().from("node:21")
      .withDirectory("/src", this.source)
      .withWorkdir("/src")
      .withExec(["cp", "-R", ".", "/home/node"])
      .withWorkdir("/home/node")
      .withExec(["npm", "install"])
      .withEntrypoint(["npm", "start"])
  }

  @func()
  async publish(project: string, location: string, repository: string, credential: Secret): Promise<string> {
    const registry = `${location}-docker.pkg.dev/${project}/${repository}`
    return await this.build()
      .withRegistryAuth(`${location}-docker.pkg.dev`, "_json_key", credential)
      .publish(registry)
  }

  @func()
  async deploy(project: string, registryLocation: string, repository: string, serviceLocation: string, service: string, credential: Secret): Promise<string> {

    const addr = await this.publish(project, registryLocation, repository, credential)

    return dag.googleCloudRun().updateService(project, serviceLocation, service, addr, 3000, credential)
  }
}
