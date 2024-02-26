import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:

    @function
    def build(self, source: dagger.Directory, arch: str, os: str) -> dagger.Container:
        dir = (
            dag.container()
            .from_("golang:1.21")
            .with_mounted_directory("/src", source)
            .with_workdir("/src")
            .with_env_variable("GOARCH", arch)
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
