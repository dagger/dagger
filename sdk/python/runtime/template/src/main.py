import dagger
from dagger import dag, function


@function
def container_echo(string_arg: str) -> dagger.Container:
    # Example usage: "dagger call container-echo --string-arg hello"
    return dag.container().from_("alpine:latest").with_exec(["echo", string_arg])


@function
async def grep_dir(directory_arg: dagger.Directory, pattern: str) -> str:
    # Example usage: "dagger call grep-dir --directory-arg . --patern grep_dir"
    return await (
        dag.container()
        .from_("alpine:latest")
        .with_mounted_directory("/mnt", directory_arg)
        .with_workdir("/mnt")
        .with_exec(["grep", "-R", pattern, "."])
        .stdout()
    )
