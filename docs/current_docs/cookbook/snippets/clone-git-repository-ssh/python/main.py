import dagger
from dagger import dag, function, object_type

@object_type
class MyModule:
    """
    Demonstrates an SSH-based clone requiring a user-supplied sshAuthSocket.
    
    For the reasoning behind explicit socket forwarding, see:
    /path/to/security-by-design
    You can also avoid passing a socket if you prefer the Directory pattern,
    e.g. dagger call someFunc --dir git@github.com:org/repo@main
    """

    @function
    async def clone_with_ssh(
        self, repository: str, ref: str, sock: dagger.Socket
    ) -> dagger.Container:
        repo_dir = dag.git(repository, ssh_auth_socket=sock).ref(ref).tree()

        return (
            dag.container()
            .from_("alpine:latest")
            .with_directory("/src", repo_dir)
            .with_workdir("/src")
        )