import { dag, Container, object, func, Socket } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
    Demonstrates an SSH-based clone requiring a user-supplied sshAuthSocket.
   */
  @func()
  cloneWithSsh(repository: string, ref: string, sock: Socket): Container {
    const repoDir = dag.git(repository, { sshAuthSocket: sock }).ref(ref).tree()

    return dag
      .container()
      .from("alpine:latest")
      .withDirectory("/src", repoDir)
      .withWorkdir("/src")
  }
}
