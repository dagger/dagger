import { dag, Directory, Container, object, func } from "@dagger.io/dagger"

export enum Locator {
  Branch = "BRANCH",
  Tag = "TAG",
  Commit = "COMMIT"
}

@object()
class MyModule {
  @func()
  clone(repository: string, locator: Locator, id: string): Container {
    const r = dag.git(repository)
    let dir: Directory

    switch (locator) {
      case Locator.Branch:
        dir = r.branch(id).tree()
        break
      case Locator.Tag:
        dir = r.tag(id).tree()
        break
      case Locator.Commit:
        dir = r.commit(id).tree()
        break
    }

    return dag.container()
      .from("alpine:latest")
      .withDirectory("/src", dir)
      .withWorkdir("/src")
  }
}
