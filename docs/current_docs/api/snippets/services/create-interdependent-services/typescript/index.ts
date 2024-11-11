import { object, func, Service } from "@dagger.io/dagger"

@object()
class MyModule {
  // Run two services which are dependent on each other
  @func()
  async services(): Promise<Service> {
    const svcA = dag
      .container()
      .from("nginx")
      .withExposedPort(80)
      .withExec([
        "sh",
        "-c",
        `nginx & while true; do curl svcb:80 && sleep 1; done`,
      ])
      .asService()
      .withHostname("svca")

    await svcA.start()

    const svcB = dag
      .container()
      .from("nginx")
      .withExposedPort(80)
      .withExec([
        "sh",
        "-c",
        `nginx & while true; do curl svca:80 && sleep 1; done`,
      ])
      .asService()
      .withHostname("svcb")

    await svcB.start()

    return svcB
  }
}
