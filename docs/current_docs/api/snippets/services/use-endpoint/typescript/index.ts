import { dag, object, func } from "@dagger.io/dagger"

@object()
export class MyModule {
  @func()
  async get(): Promise<string> {
    // start NGINX service
    let service = dag.container().from("nginx").withExposedPort(80).asService()
    service = await service.start()

    // wait for service to be ready
    const endpoint = await service.endpoint({ port: 80, scheme: "http" })

    // send HTTP request to service endpoint
    return await dag.http(endpoint).contents()
  }
}
