import { dag, object, func } from "@dagger.io/dagger"

@object()
export class MyModule {
  @func()
  async get(): Promise<string> {
    // start NGINX service
    let svc = dag.container().from("nginx").withExposedPort(80).asService()
    svc = await svc.start()

    // wait for service to be ready
    let ep = await svc.endpoint({"port": 80, "scheme": "http"})

    // send HTTP request to service endpoint
    return await dag.http(ep).contents()
  }
}
