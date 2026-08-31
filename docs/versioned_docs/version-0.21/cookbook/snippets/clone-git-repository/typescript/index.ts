import { dag, Directory, Container, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
    Demonstrates cloning a Git repository over HTTP(S).

    For SSH usage, see the SSH snippet (cloneWithSsh).
   */
  @func()
  clone(repository: string, ref: string): Container {
    const repoDir = dag.git(repository, { sshAuthSocket: sock }).ref(ref).tree()

    return dag
      .container()
      .from("alpine:latest")
      .withDirectory("/src", repoDir)
      .withWorkdir("/src")
  }
}
