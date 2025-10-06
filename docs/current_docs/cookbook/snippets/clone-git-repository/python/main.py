import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    """
    Demonstrates cloning a Git repository over HTTP(S).

    For SSH usage, see the SSH snippet (clone_with_ssh).
    """

    @function
    async def clone(
        self,
        repository: str,
        ref: str,
    ) -> dagger.Container:
        repo_dir = dag.git(repository).ref(ref).tree()

        return (
            dag.container()
            .from_("alpine:latest")
            .with_directory("/src", repo_dir)
            .with_workdir("/src")
        )
