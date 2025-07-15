import { dag, Container, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  alpineBuilder(packages: string[]): Container {
    let ctr = dag.container().from("alpine:latest")
    for (const pkg in packages) {
      ctr = ctr.withExec(["apk", "add", pkg])
    }
    return ctr
  }
}
