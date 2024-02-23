import dagger
from dagger import dag, object_type, function

@object_type
class MyModule:

    @function
    def build(source: dagger.Directory, architecture: str, os: str) -> dagger.Container:

        dir = (
            dag.container()
            .from_("golang:1.21")
            .with_mounted_directory("/src", source)
            .with_workdir("/src")
            .with_env_variable("GOARCH", architecture)
            .with_env_variable("GOOS", os)
            .with_env_variable("CGO_ENABLED", "0")
            .with_exec(["go", "build", "-o", "build/"])
            .directory("/src/build")
        )

        return (
            dag.container()
            .from_("alpine:latest")
            .with_directory("/usr/local/bin", dir)
        )
