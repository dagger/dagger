import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def go_builder(self, src: dagger.Directory, arch: str, os: str) -> dagger.Directory:
        return (
            dag.container()
            .from_("golang:1.21")
            .with_mounted_directory("/src", src)
            .with_workdir("/src")
            .with_env_variable("GOARCH", arch)
            .with_env_variable("GOOS", os)
            .with_env_variable("CGO_ENABLED", "0")
            .with_exec(["go", "build", "-o", "build/"])
            .directory("/src/build")
        )
