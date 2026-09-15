import { ContainerID, dag, func, object } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  async roundTrip(): Promise<string> {
    const id = await dag.container().from("alpine:3.22.1").id()
    return await dag.loadContainerFromID(id as ContainerID).withExec(["echo", "ok"]).stdout()
  }
}
