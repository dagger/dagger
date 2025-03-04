import { dag, Directory, Container, object, func } from "@dagger.io/dagger"

export enum Locator {
  Branch = "BRANCH",
  Tag = "TAG",
  Commit = "COMMIT",
}

@object()
class MyModule {
  /**
    Demonstrates cloning a Git repository over HTTP(S).
   
    For SSH usage, see the SSH snippet (cloneWithSsh).
   */
  @func()
  clone(repository: string, locator: Locator, ref: string): Container {
    const r = dag.git(repository)
    let d: Directory

    switch (locator) {
      case Locator.Branch:
        d = r.branch(ref).tree()
        break
      case Locator.Tag:
        d = r.tag(ref).tree()
        break
      case Locator.Commit:
        d = r.commit(ref).tree()
        break
    }

    return dag
      .container()
      .from("alpine:latest")
      .withDirectory("/src", d)
      .withWorkdir("/src")
  }
}
