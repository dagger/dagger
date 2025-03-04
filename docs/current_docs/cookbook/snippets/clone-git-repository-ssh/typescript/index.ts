import { dag, Directory, Container, object, func, Socket } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
    Demonstrates an SSH-based clone requiring a user-supplied sshAuthSocket.
    
    For the reasoning behind explicit socket forwarding, see:
    /path/to/security-by-design
    You can also avoid passing a socket if you prefer the Directory pattern,
    e.g. dagger call someFunc --dir git@github.com:org/repo@main
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
