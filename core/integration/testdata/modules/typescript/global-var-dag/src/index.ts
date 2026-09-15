import { dag, object, func } from "@dagger.io/dagger"

var someDefault = dag.container().from("alpine:3.22.1")

@object()
export class Test {
  @func()
  async fn(): Promise<string> {
    return someDefault.withExec(["echo", "foo"]).stdout()
  }
}
